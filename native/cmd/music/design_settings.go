package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"tinglan/native/internal/lyrics"
	"tinglan/native/internal/music"
	"tinglan/native/internal/plugin"
	"tinglan/native/internal/source"
	"tinglan/native/internal/storage"
)

type designSourceRow struct {
	ID, Name, Version, Capabilities, LoginLabel string
	Enabled, External, SignedIn, CanLogin       bool
}

type designPreferences struct {
	Sources                                          []designSourceRow
	SourceReady, SourceBusy, AccountBusy, StoreReady bool
	Message, Status                                  string
	CloseToTray, Lossless, ThirdPartyLyrics          bool
	AudioCacheMiB                                    int
	AudioCacheBytes                                  int64
	Mode, FontSize, Theme                            int
	FontFamily                                       string
	LyricsTarget                                     string
	LyricStats                                       storage.LyricStats
	JevEnabled, JevTesting                           bool
	JevEndpoint, JevModel, JevStatus                 string
	JevSaved                                         int
}

type designSourceResult struct {
	action  string
	id      music.SourceID
	entry   plugin.Entry
	entries []plugin.Entry
	err     error
}

type designSettingsSession struct {
	cancel   context.CancelFunc
	work     <-chan designSourceResult
	message  string
	jevSaved int
}

// The bridge alone owns each controller's mutable fields. Workers only send
// results; the bridge joins them before the model closes its profile.
var designSettingsSessions sync.Map // *model -> *designSettingsSession

func designSettingsFor(m *model) *designSettingsSession {
	if s, ok := designSettingsSessions.Load(m); ok {
		return s.(*designSettingsSession)
	}
	s := new(designSettingsSession)
	designSettingsSessions.Store(m, s)
	return s
}

func designSettingsClose(m *model) {
	if s, ok := designSettingsSessions.LoadAndDelete(m); ok {
		p := s.(*designSettingsSession)
		if p.work != nil {
			p.cancel()
			<-p.work
		}
	}
}

func designSettingsRows(m *model) []designSourceRow {
	rows := []designSourceRow{}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.sourceRows() {
		row := designSourceRow{ID: string(r.id), Name: r.name, Enabled: r.enabled}
		if e, ok := m.pluginEntry(r.id); ok {
			row.External = !e.Manifest.Builtin
			row.Version = e.Manifest.Version
			row.Capabilities = capabilitySummary(e.Descriptor.Capabilities)
		}
		if src, ok := m.registrySource(r.id); ok && r.enabled && slices.Contains(src.Describe().Capabilities.LoginKinds, source.LoginWebView) {
			row.SignedIn = m.sourceSignedIn(r.id)
			row.CanLogin = !m.restoring && (!m.busy || m.loginTarget == r.id)
			switch {
			case m.busy && m.loginTarget == r.id:
				row.LoginLabel = "取消登录"
			case row.SignedIn:
				row.LoginLabel = "重新登录"
			default:
				row.LoginLabel = "登录"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// Keep GTK preferences in the existing appearance field without interpreting Viva palette names.
var designThemeModes = [...]string{"light", "ice", "graphite"}

func designSettingsState(m *model) any {
	p := designSettingsFor(m)
	p.drain(m)
	s, a := m.settings, m.settings.Appearance
	fontSize := int(a.FontScale*100 + .5)
	if fontSize == 0 {
		fontSize = 100
	}
	mode := map[string]int{"all": 1, "one": 2}[s.RepeatMode]
	if s.Shuffle {
		mode = 3
	}
	theme := slices.Index(designThemeModes[:], a.Theme)
	if theme < 0 {
		theme = 0
	}
	snapshot := designPreferences{
		Sources: designSettingsRows(m), SourceReady: m.plugins != nil && m.registry != nil, SourceBusy: p.work != nil,
		AccountBusy: m.busy || m.restoring || m.importing, StoreReady: m.store != nil && !m.demo,
		Status: m.status, Message: p.message, CloseToTray: s.CloseToTray, Lossless: s.Quality == "lossless",
		AudioCacheMiB: s.AudioCacheMiB, ThirdPartyLyrics: s.ThirdPartyLyrics, Mode: mode, FontSize: fontSize, Theme: theme,
		FontFamily: a.FontFamily, LyricStats: m.lyricStats,
		JevEnabled: s.Jev.Enabled, JevEndpoint: s.Jev.Endpoint, JevModel: s.Jev.Model, JevTesting: m.jevTesting,
		JevStatus: m.jevTestStatus, JevSaved: p.jevSaved,
	}
	if m.audioCache != nil {
		snapshot.AudioCacheBytes = m.audioCache.UsageBytes()
	}
	if cur := m.queue.Current(); cur != nil {
		snapshot.LyricsTarget = string(cur.Source) + ":" + cur.ID
	}
	return snapshot
}

func (p *designSettingsSession) start(work func(context.Context) designSourceResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	ch := make(chan designSourceResult, 1)
	p.cancel, p.work, p.message = cancel, ch, "正在处理音源…"
	go func() {
		result := work(ctx)
		cancel()
		ch <- result // The bridge drains the result at the next state poll.
	}()
}

func (p *designSettingsSession) drain(m *model) {
	if p.work == nil {
		return
	}
	var result designSourceResult
	select {
	case result = <-p.work:
	default:
		return
	}
	p.work, p.cancel = nil, nil
	m.pluginEntries = result.entries
	if result.err != nil {
		p.message = "音源操作失败：" + result.err.Error()
		return
	}
	switch result.action {
	case "install":
		m.setSourceEnabled(result.id, false)
		p.message = "已添加 " + result.entry.Name() + "；可在列表中启用"
	case "enable", "disable":
		on := result.action == "enable"
		m.setSourceEnabled(result.id, on)
		if on {
			p.message = "已启用 "
			m.refreshSourceAuth(result.id)
		} else {
			p.message = "已停用 "
		}
		p.message += m.sourceDisplayName(result.id)
		designSettingsRefreshSearch(m)
	case "refresh":
		m.refreshAllSourceAuth()
		p.message = "已刷新音源列表"
	}
}

func designSettingsRefreshSearch(m *model) {
	m.clampSearchSource()
	if m.searchQuery != "" {
		m.search()
	}
}

func (p *designSettingsSession) source(m *model, c designCommand) {
	if p.work != nil {
		return
	}
	p.message = ""
	id := music.SourceID(c.ID)
	manager, registry := m.plugins, m.registry
	if c.Op == "settings-source-folder" || c.Op == "settings-source-refresh" || c.Op == "settings-source-install" {
		if manager == nil || registry == nil {
			p.message = "音源管理器未就绪"
			return
		}
		switch c.Op {
		case "settings-source-folder":
			m.openPluginFolder()
			p.message = m.toast
		case "settings-source-refresh":
			p.start(func(context.Context) designSourceResult {
				return designSourceResult{action: "refresh", entries: manager.List()}
			})
		case "settings-source-install":
			if c.Index != 1 || c.Text == "" {
				return
			}
			if m.busy || m.restoring || m.importing {
				p.message = "请等待当前登录或收藏导入完成"
				return
			}
			p.start(func(ctx context.Context) designSourceResult {
				entry, err := designInstallSource(ctx, m, manager, registry, c.Text)
				return designSourceResult{action: "install", id: entry.ID(), entry: entry, err: err, entries: manager.List()}
			})
		}
		return
	}
	var row designSourceRow
	for _, r := range designSettingsRows(m) {
		if r.ID == c.ID {
			row = r
			break
		}
	}
	if row.ID == "" {
		p.message = "找不到音源"
		return
	}
	entry, managed := m.pluginEntry(id)
	switch c.Op {
	case "settings-source-login":
		if !row.CanLogin || row.LoginLabel == "" {
			return
		}
		if m.busy && m.loginTarget == id {
			m.cancelLogin()
			return
		}
		m.loginTarget = id
		m.startLogin()
	case "settings-source-logout":
		if c.Index != 1 || !row.SignedIn || m.busy || m.restoring || m.importing {
			return
		}
		account := id == m.accountSourceID()
		m.logoutSource(id)
		if account {
			m.mu.Lock()
			delete(m.sourceSessions, id)
			delete(m.sourceAuth, id)
			m.mu.Unlock()
		} else {
			p.message = m.toast
		}
	case "settings-source-enable", "settings-source-disable":
		on := c.Op == "settings-source-enable"
		if on == row.Enabled {
			return
		}
		if m.busy || m.restoring || m.importing {
			p.message = "请等待当前登录或收藏导入完成"
			return
		}
		if !managed {
			m.setSourceEnabled(id, on)
			m.refreshSourceAuth(id)
			designSettingsRefreshSearch(m)
			return
		}
		if manager == nil || registry == nil || (on && !entry.Manifest.Builtin && c.Index != 1) {
			return
		}
		p.start(func(ctx context.Context) designSourceResult {
			result := designSourceResult{id: id, action: "disable"}
			if on {
				result.action = "enable"
				if !entry.Manifest.Builtin {
					result.err = manager.MarkConsented(id)
				}
				if result.err == nil {
					result.err = manager.Enable(ctx, registry, id)
				}
			} else {
				result.err = manager.Disable(registry, id)
			}
			result.entries = manager.List()
			return result
		})
	}
}

func designInstallSource(ctx context.Context, m *model, manager *plugin.Manager, registry *source.Registry, path string) (plugin.Entry, error) {
	dir, cleanup, err := m.preparePluginSource(path)
	if err != nil {
		return plugin.Entry{}, err
	}
	defer cleanup()
	manifest, err := plugin.LoadManifest(dir)
	if err != nil {
		return plugin.Entry{}, err
	}
	if manifest.Builtin {
		return plugin.Entry{}, errors.New("只可导入外置音源")
	}
	id := music.SourceID(manifest.ID)
	if _, ok := registry.Source(id); ok {
		return plugin.Entry{}, errors.New("请先停用同 ID 的音源再更新")
	}
	for _, entry := range manager.List() {
		if entry.ID() == id && (entry.Manifest.Builtin || entry.Enabled || entry.Running) {
			return plugin.Entry{}, errors.New("不能覆盖内置或启用的音源")
		}
	}
	if err := ctx.Err(); err != nil {
		return plugin.Entry{}, err
	}
	entry, _, err := manager.Install(ctx, dir)
	return entry, err
}

func designSettingsCommand(m *model, c designCommand) bool {
	if !strings.HasPrefix(c.Op, "settings-") {
		return false
	}
	if m.closed {
		return true
	}
	p := designSettingsFor(m)
	p.drain(m)
	if strings.HasPrefix(c.Op, "settings-source-") {
		p.source(m, c)
		return true
	}
	switch c.Op {
	case "settings-close":
		m.settings.CloseToTray = c.Index == 1
	case "settings-audio-cache":
		if c.Index < 0 || c.Index > storage.MaxAudioCacheMiB {
			return true
		}
		m.settings.AudioCacheMiB = c.Index
		if c.Index == 0 {
			m.cancelAudioCache()
		}
		if m.audioCache != nil {
			m.audioCache.SetLimit(int64(c.Index) << 20)
		}
	case "settings-quality":
		if c.Index == 1 {
			m.settings.Quality = "lossless"
		} else {
			m.settings.Quality = "standard"
		}
	case "settings-mode":
		modes := [...]string{"off", "all", "one", "off"}
		if c.Index < 0 || c.Index >= len(modes) {
			return true
		}
		m.settings.RepeatMode, m.settings.Shuffle = modes[c.Index], c.Index == 3
		m.queue.SetShuffle(m.settings.Shuffle)
	case "settings-lyrics-match":
		m.settings.ThirdPartyLyrics = c.Index == 1
	case "settings-font-size":
		m.settings.Appearance.FontScale = float64(c.Index) / 100
	case "settings-font-family":
		m.settings.Appearance.FontFamily = strings.TrimSpace(c.Text)
	case "settings-theme":
		if c.Index < 0 || c.Index >= len(designThemeModes) {
			return true
		}
		m.settings.Appearance.Theme = designThemeModes[c.Index]
	case "settings-appearance-reset":
		m.settings.Appearance = storage.Defaults().Appearance
	case "settings-jev-enabled":
		m.settings.Jev.Enabled = c.Index == 1
	case "settings-jev-save", "settings-jev-test":
		var form struct{ Endpoint, Model, Key string }
		if m.store == nil || json.Unmarshal([]byte(c.Text), &form) != nil {
			return true
		}
		m.jevEndpointField.SetText(form.Endpoint)
		m.jevModelField.SetText(form.Model)
		m.jevKeyField.SetText(form.Key)
		if c.Op == "settings-jev-save" {
			m.saveJevConfig()
			if strings.HasPrefix(m.status, "决策模型配置已保存") && m.jevKeyField.Text() == "" {
				p.jevSaved = c.Index
			}
		} else {
			m.testJevConfig()
		}
		// The key entry is write-only at the bridge and not retained here.
		m.jevKeyField.SetText("")
		return true
	case "settings-lyrics-clear-network", "settings-lyrics-clear-local":
		if m.store == nil || c.Index != 1 {
			return true
		}
		local := c.Op == "settings-lyrics-clear-local"
		if err := m.store.ClearLyrics(local); err != nil {
			m.setStatus("歌词清理失败：" + err.Error())
			return true
		}
		m.designLyrics.invalidate()
		m.lyricStats = m.store.LyricsInfo()
		if local && m.lyricSource == "本地导入" {
			m.lyricLines, m.lyricSource = nil, ""
		}
		m.setStatus("歌词已清理")
		return true
	case "settings-lyrics-import", "settings-lyrics-remove":
		cur := m.queue.Current()
		if m.store == nil || cur == nil || c.ID != string(cur.Source)+":"+cur.ID {
			m.setStatus("播放曲目已变更，请重试")
			return true
		}
		if c.Op == "settings-lyrics-import" {
			m.setStatus(designImportLyrics(m, c.Text))
		} else if c.Index == 1 {
			key := m.trackLyricsKey(*cur)
			if err := m.store.RemoveLocalLyrics(key); err != nil {
				m.setStatus("移除歌词失败")
				return true
			}
			m.designLyrics.invalidate()
			m.lyricLines, m.lyricSource = nil, ""
			if doc, err := m.store.LoadLyrics(key); err == nil {
				m.lyricLines, m.lyricSource = doc.Lines, doc.Source
				if len(m.lyricLines) == 0 {
					m.lyricLines, _ = lyrics.ParseAny(doc.Text)
				}
			}
			m.setStatus("已移除本曲导入歌词")
		}
		m.lyricStats = m.store.LyricsInfo()
		return true
	default:
		return false
	}
	m.saveSettings()
	return true
}
