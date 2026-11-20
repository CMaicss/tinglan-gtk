//go:build linux && gtk && cgo

package main

/*
#include <stdint.h>
#include <stdlib.h>
uintptr_t tl_settings_state(void);
int tl_settings_mapped(void);
void tl_settings_reset_sources(void);
void tl_settings_add_source(const char *, const char *, const char *, int, const char *, int, int);
void tl_settings_status(const char *, int);
void tl_settings_preferences(int, int, int, int, int, int, int, const char *, int);
void tl_settings_jev(const char *, const char *, const char *, int);
void tl_settings_apply_appearance(const char *, int);
void tl_settings_confirm(const char *, const char *, const char *, const char *, const char *);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/cgo"
	"slices"
	"strings"
	"time"
	"unicode"
	"unsafe"

	"tinglan/native/internal/lyrics"
	"tinglan/native/internal/music"
	"tinglan/native/internal/plugin"
	"tinglan/native/internal/source"
	"tinglan/native/internal/storage"
)

type gtkSettings struct {
	session       *gtkSession
	rows          []gtkSettingsRow
	refreshRows   bool
	appearance    storage.Appearance
	appearanceSet bool
	result        chan gtkSettingsResult
	cancel        context.CancelFunc
	message       string
	lyricTarget   string
}

type gtkSettingsRow struct {
	id                          music.SourceID
	name, subtitle, login       string
	enabled, signedIn, canLogin bool
}

type gtkSettingsResult struct {
	action  string
	id      music.SourceID
	entry   plugin.Entry
	entries []plugin.Entry
	err     error
}

//export tlGoSettingsInit
func tlGoSettingsInit(session C.uintptr_t) C.uintptr_t {
	return C.uintptr_t(cgo.NewHandle(&gtkSettings{session: gtkFrom(session), refreshRows: true}))
}

//export tlGoSettingsHideOnClose
func tlGoSettingsHideOnClose(handle C.uintptr_t) C.int {
	m := cgo.Handle(handle).Value().(*gtkSettings).session.model
	return gtkSettingsBool(!m.demo && !m.closed && m.settings.CloseToTray)
}

//export tlGoSettingsDestroy
func tlGoSettingsDestroy(handle C.uintptr_t) {
	h := cgo.Handle(handle)
	p := h.Value().(*gtkSettings)
	if p.cancel != nil {
		p.cancel()
	}
	h.Delete()
}

func gtkSettingsString(s string, fn func(*C.char)) {
	text := C.CString(s)
	defer C.free(unsafe.Pointer(text))
	fn(text)
}

func gtkSettingsBool(ok bool) C.int {
	if ok {
		return 1
	}
	return 0
}

func (s *gtkSession) renderSettings() {
	handle := C.tl_settings_state()
	if handle == 0 {
		return
	}
	p := cgo.Handle(handle).Value().(*gtkSettings)
	p.finishSourceWork()
	m := s.model
	if !p.appearanceSet || p.appearance != m.settings.Appearance {
		p.appearance, p.appearanceSet = m.settings.Appearance, true
		a, palette := m.settings.Appearance, m.palette()
		var family string
		if a.FontFamily != "" {
			family = `font-family: "` + strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return -1
				}
				return r
			}, strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a.FontFamily)) + `";`
		}
		css := fmt.Sprintf("@define-color accent_color #%06x; @define-color accent_bg_color #%06x; window.tinglan-settings-style { font-size: %.2fem; %s }", palette.accent, palette.accent, a.FontScale, family)
		// Leave the default appearance under the desktop's control.
		scheme := C.int(0)
		if palette.dark {
			scheme = 2
		} else if a.Theme != "" && a.Theme != "light" {
			scheme = 1
		}
		gtkSettingsString(css, func(v *C.char) { C.tl_settings_apply_appearance(v, scheme) })
	}
	if C.tl_settings_mapped() == 0 {
		return
	}
	rows := gtkSettingsRows(m)
	if p.refreshRows || !slices.Equal(p.rows, rows) {
		C.tl_settings_reset_sources()
		for _, row := range rows {
			gtkSettingsString(string(row.id), func(id *C.char) {
				gtkSettingsString(row.name, func(name *C.char) {
					gtkSettingsString(row.subtitle, func(subtitle *C.char) {
						gtkSettingsString(row.login, func(login *C.char) {
							C.tl_settings_add_source(id, name, subtitle, gtkSettingsBool(row.enabled), login, gtkSettingsBool(row.signedIn), gtkSettingsBool(row.canLogin))
						})
					})
				})
			})
		}
		p.rows, p.refreshRows = rows, false
	}
	status := p.message
	if m.toastVisible() {
		status = m.toast
	}
	gtkSettingsString(status, func(v *C.char) { C.tl_settings_status(v, gtkSettingsBool(p.result != nil)) })
	mode := map[string]int{"off": 0, "all": 1, "one": 2}[m.settings.RepeatMode]
	if m.settings.Shuffle {
		mode = 3
	}
	family := m.settings.Appearance.FontFamily
	if family == "" {
		family = "跟随系统"
	}
	gtkSettingsString(family, func(v *C.char) {
		C.tl_settings_preferences(gtkSettingsBool(m.settings.CloseToTray), gtkSettingsBool(m.settings.Quality == "lossless"), C.int(mode), gtkSettingsBool(m.settings.ThirdPartyLyrics), C.int(100*m.settings.Appearance.FontScale+.5), C.int(max(0, slices.Index(themeNames, m.settings.Appearance.Theme))), gtkSettingsBool(m.settings.Jev.Enabled), v, gtkSettingsBool(m.queue.Current() != nil && m.store != nil))
	})
	jevMessage := m.jevTestStatus
	if jevMessage == "" {
		jevMessage = m.jevError
	}
	gtkSettingsString(m.jevEndpointField.Text(), func(endpoint *C.char) {
		gtkSettingsString(m.jevModelField.Text(), func(model *C.char) {
			gtkSettingsString(jevMessage, func(status *C.char) { C.tl_settings_jev(endpoint, model, status, gtkSettingsBool(m.jevTesting)) })
		})
	})
}

func gtkSettingsRows(m *model) []gtkSettingsRow {
	// Auth status is delivered by the shared model's background plugin RPC.
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []gtkSettingsRow
	for _, r := range m.sourceRows() {
		row := gtkSettingsRow{id: r.id, name: r.name, enabled: r.enabled, subtitle: "已停用"}
		loginable := r.loginable
		if src, ok := m.registrySource(r.id); ok {
			desc := src.Describe()
			if desc.Name != "" {
				row.name = desc.Name
			}
			loginable = slices.Contains(desc.Capabilities.LoginKinds, source.LoginWebView)
		}
		if entry, ok := m.pluginEntry(r.id); ok && entry.Manifest.Version != "" {
			row.subtitle = entry.Manifest.Version + " · " + row.subtitle
		}
		if r.enabled {
			row.subtitle = strings.TrimSuffix(row.subtitle, "已停用") + "已启用"
		}
		if r.enabled && loginable {
			row.signedIn = m.sourceSignedIn(r.id)
			row.canLogin = !m.busy || m.loginTarget == r.id
			switch {
			case m.busy && m.loginTarget == r.id:
				row.subtitle += " · 正在登录…"
				row.login = "取消登录"
				row.signedIn = false
			case row.signedIn:
				row.subtitle += " · 已登录"
				row.login = "重新登录"
			default:
				row.subtitle += " · 未登录"
				row.login = "登录"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func gtkSettingsConfirm(action, value, title, body, affirmative string) {
	gtkSettingsString(action, func(a *C.char) {
		gtkSettingsString(value, func(v *C.char) {
			gtkSettingsString(title, func(t *C.char) {
				gtkSettingsString(body, func(b *C.char) {
					gtkSettingsString(affirmative, func(ok *C.char) { C.tl_settings_confirm(a, v, t, b, ok) })
				})
			})
		})
	})
}

func (p *gtkSettings) askEnable(entry plugin.Entry) {
	detail := entry.Name()
	if entry.Manifest.Version != "" {
		detail += " " + entry.Manifest.Version
	}
	if summary := capabilitySummary(entry.Descriptor.Capabilities); summary != "" {
		detail += "\n能力：" + summary
	}
	detail += "\n\n外置音源以你的用户权限运行。只启用你信任的程序。"
	gtkSettingsConfirm("enable", string(entry.ID()), "启用外置音源", detail, "启用")
}

//export tlGoSettingsAction
func tlGoSettingsAction(handle C.uintptr_t, rawAction, rawValue, rawEndpoint, rawModel, rawKey *C.char, number C.int) {
	p := cgo.Handle(handle).Value().(*gtkSettings)
	m := p.session.model
	action, value, n := C.GoString(rawAction), C.GoString(rawValue), int(number)
	if m.closed {
		return
	}
	defer m.requestUpdate()
	switch action {
	case "mapped":
		// The page became visible even if the player is idle.
	case "nonlocal-source":
		m.notify("请选择本机的音源文件或文件夹")
	case "refresh":
		if p.result == nil && m.plugins != nil {
			manager := m.plugins
			p.runSourceWork(func(context.Context) gtkSettingsResult {
				return gtkSettingsResult{action: action, entries: manager.List()}
			})
		}
	case "source-toggle", "enable", "source-login", "source-logout":
		p.sourceAction(action, music.SourceID(value))
	case "import-picked":
		if p.result == nil && value != "" {
			gtkSettingsConfirm("install", value, "添加外置音源", "为了识别这个音源，听澜需要运行其中的程序一次，程序拥有你的用户权限。只添加来自可信来源的音源。\n\n"+value, "运行并添加")
		}
	case "install":
		if !m.busy && !m.restoring && !m.importing {
			p.runSourceWork(func(ctx context.Context) gtkSettingsResult {
				entry, err := gtkInstallSource(ctx, m, value, true)
				return gtkSettingsResult{action: action, id: entry.ID(), entry: entry, err: err}
			})
		} else {
			m.notify("请等待当前登录或收藏导入完成")
		}
	case "open-source-folder":
		if m.plugins != nil {
			m.openPluginFolder()
		} else {
			m.notify("音源管理器未就绪")
		}
	case "close", "quality", "mode", "lyrics-match", "font-size", "font-family", "theme", "appearance-reset", "jev-enabled":
		gtkUpdatePreference(m, action, value, n)
	case "jev-save", "jev-test":
		m.jevEndpointField.SetText(C.GoString(rawEndpoint))
		m.jevModelField.SetText(C.GoString(rawModel))
		m.jevKeyField.SetText(C.GoString(rawKey))
		if action == "jev-save" {
			m.saveJevConfig()
		} else {
			m.testJevConfig()
		}
		if action == "jev-save" && m.jevKeyField.Text() == "" {
			gtkSettingsString("", func(empty *C.char) { C.tl_settings_jev(nil, nil, empty, -1) })
		}
	case "lyrics-clear-network", "lyrics-clear-local":
		if action == "lyrics-clear-local" && n == 0 {
			gtkSettingsConfirm(action, "", "清除全部本地导入歌词", "只删除导入的歌词文件，不影响收藏或播放队列。", "清除")
			return
		}
		if m.store == nil {
			m.notify("歌词存储未就绪")
			return
		}
		local := action == "lyrics-clear-local"
		if err := m.store.ClearLyrics(local); err != nil {
			m.setStatus("歌词清理失败：" + err.Error())
			return
		}
		m.lyricStats = m.store.LyricsInfo()
		if local && m.lyricSource == "本地导入" {
			m.lyricLines = nil
			m.lyricSource = ""
		}
		if local {
			m.setStatus("本地导入歌词已清除")
		} else {
			m.setStatus("网络歌词缓存已清除")
		}
	case "lyrics-pick-start":
		p.lyricTarget = ""
		if cur := m.queue.Current(); cur != nil {
			p.lyricTarget = cur.ID
		}
	case "lyrics-picked":
		if p.lyricTarget != "" && value != "" {
			m.setStatus(gtkImportLyrics(m, p.lyricTarget, value))
		}
		p.lyricTarget = ""
	case "lyrics-remove":
		m.removeLyrics()
	}
}

func gtkUpdatePreference(m *model, action, value string, n int) {
	switch action {
	case "close":
		m.settings.CloseToTray = n == 1
	case "quality":
		if n == 1 {
			m.settings.Quality = "lossless"
		} else {
			m.settings.Quality = "standard"
		}
	case "mode":
		modes := [...]string{"off", "all", "one", "off"}
		if n < 0 || n >= len(modes) {
			return
		}
		m.settings.RepeatMode = modes[n]
		m.settings.Shuffle = n == 3
		m.queue.SetShuffle(m.settings.Shuffle)
	case "lyrics-match":
		m.settings.ThirdPartyLyrics = n != 0
	case "font-size":
		m.settings.Appearance.FontScale = float64(n) / 100
	case "font-family":
		m.settings.Appearance.FontFamily = value
	case "theme":
		if n >= 0 && n < len(themeNames) {
			m.settings.Appearance.Theme = themeNames[n]
		}
	case "appearance-reset":
		m.settings.Appearance = storage.Defaults().Appearance
	case "jev-enabled":
		m.settings.Jev.Enabled = n != 0
	default:
		return
	}
	m.saveSettings()
}

func (p *gtkSettings) sourceAction(action string, id music.SourceID) {
	m := p.session.model
	if p.result != nil {
		return
	}
	var row gtkSettingsRow
	for _, candidate := range gtkSettingsRows(m) {
		if candidate.id == id {
			row = candidate
			break
		}
	}
	if row.id == "" {
		return
	}
	entry, external := m.pluginEntry(id)
	switch action {
	case "source-login":
		if row.login == "" || !row.canLogin {
			return
		}
		if m.busy && m.loginTarget == id {
			m.cancelLogin()
			return
		}
		m.loginTarget = id
		m.startLogin()
	case "source-logout":
		if !m.busy && row.signedIn {
			account := id == m.accountSourceID()
			m.logoutSource(id)
			if account {
				m.mu.Lock()
				delete(m.sourceAuth, id)
				delete(m.sourceSessions, id)
				m.mu.Unlock()
				m.refreshSourceAuth(id)
			}
		}
	case "source-toggle", "enable":
		if m.restoring || (m.busy && m.loginTarget == id) || m.importing {
			m.notify("请等待当前登录或收藏导入完成")
			return
		}
		if !external {
			if action == "source-toggle" {
				m.toggleSource(id)
				p.refreshSearch()
			}
			return
		}
		manager, registry := m.plugins, m.registry
		if manager == nil || registry == nil {
			return
		}
		if row.enabled {
			if action == "source-toggle" {
				p.runSourceWork(func(context.Context) gtkSettingsResult {
					return gtkSettingsResult{action: "disable", id: id, err: manager.Disable(registry, id)}
				})
			}
			return
		}
		if action != "enable" {
			// Confirm every start from settings: an executable may have been
			// replaced since this ID was previously authorized.
			p.askEnable(entry)
			return
		}
		p.runSourceWork(func(ctx context.Context) gtkSettingsResult {
			return gtkSettingsResult{action: "enable", id: id, err: gtkEnableSource(ctx, manager, registry, id, action == "enable")}
		})
	}
}

func (p *gtkSettings) runSourceWork(work func(context.Context) gtkSettingsResult) {
	if p.result != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	ch := make(chan gtkSettingsResult, 1)
	p.result, p.cancel = ch, cancel
	p.message = "正在处理音源…"
	m := p.session.model
	go func() { defer cancel(); ch <- work(ctx); m.requestUpdate() }()
}

func (p *gtkSettings) finishSourceWork() {
	if p.result == nil {
		return
	}
	var result gtkSettingsResult
	select {
	case result = <-p.result:
	default:
		return
	}
	p.result, p.cancel, p.message = nil, nil, ""
	m := p.session.model
	if m.closed {
		return
	}
	if result.action == "refresh" {
		m.pluginEntries = result.entries
		return
	}
	m.refreshPlugins()
	if result.err != nil {
		p.message = "音源操作失败：" + result.err.Error()
		m.notify(p.message)
		return
	}
	switch result.action {
	case "install":
		m.setSourceEnabled(result.id, false)
		p.message = "已添加 " + result.entry.Name()
		if C.tl_settings_mapped() != 0 {
			p.askEnable(result.entry)
		}
	case "enable", "disable":
		on := result.action == "enable"
		m.setSourceEnabled(result.id, on)
		if on {
			m.refreshSourceAuth(result.id)
			p.message = "已启用 "
		} else {
			p.message = "已停用 "
		}
		p.message += m.sourceDisplayName(result.id)
	}
	p.refreshSearch()
	m.notify(p.message)
}

func (p *gtkSettings) refreshSearch() {
	m := p.session.model
	m.clampSearchSource()
	if m.searchQuery != "" {
		m.search()
	}
}

// Manager.Install performs a real executable handshake. Probe consent must
// precede it, even when replacing an already installed plugin.
func gtkInstallSource(ctx context.Context, m *model, path string, consent bool) (plugin.Entry, error) {
	if !consent {
		return plugin.Entry{}, errors.New("未授权运行音源探测")
	}
	if m.plugins == nil || m.registry == nil {
		return plugin.Entry{}, errors.New("音源管理器未就绪")
	}
	dir, cleanup, err := m.preparePluginSource(path)
	if err != nil {
		return plugin.Entry{}, err
	}
	defer cleanup()
	manifest, err := plugin.LoadManifest(dir)
	if err != nil {
		return plugin.Entry{}, err
	}
	// Bundled manifests opt into automatic startup in Manager.LoadEnabled.
	if manifest.Builtin {
		return plugin.Entry{}, errors.New("不能将标记为内置的音源作为外置音源导入")
	}
	for _, e := range m.plugins.List() {
		if e.ID() == music.SourceID(manifest.ID) && e.Manifest.Builtin {
			return plugin.Entry{}, errors.New("不能覆盖内置音源")
		}
	}
	if err := ctx.Err(); err != nil {
		return plugin.Entry{}, err
	}
	entry, updated, err := m.plugins.Install(ctx, dir)
	if err != nil {
		return plugin.Entry{}, err
	}
	if updated {
		// Install shuts down a replaced process but does not unregister it or
		// remove its prior enabled state. The new executable starts disabled.
		if err := m.plugins.Disable(m.registry, entry.ID()); err != nil {
			return entry, err
		}
		m.registry.UnregisterSource(entry.ID())
	}
	return entry, nil
}

func gtkEnableSource(ctx context.Context, manager *plugin.Manager, registry *source.Registry, id music.SourceID, consent bool) error {
	if consent {
		if err := manager.MarkConsented(id); err != nil {
			return fmt.Errorf("无法记录授权：%w", err)
		}
	} else if !manager.Consented(id) {
		return errors.New("未授权启用音源")
	}
	return manager.Enable(ctx, registry, id)
}

func gtkImportLyrics(m *model, id, path string) string {
	if m.store == nil {
		return "歌词存储未就绪"
	}
	f, err := os.Open(path)
	if err != nil {
		return "无法读取歌词文件"
	}
	defer f.Close()
	const limit = 512 * 1024
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return "歌词文件不可用或超过 512KB"
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(data) > limit {
		return "歌词文件不可用或超过 512KB"
	}
	text, err := lyrics.Decode(data)
	if err != nil {
		return "歌词编码无法识别"
	}
	lines, format := lyrics.ParseAny(text)
	if len(lines) == 0 {
		return "歌词文件没有可用内容"
	}
	doc := storage.LyricDocument{Text: text, Source: "本地导入"}
	if err := m.store.SaveLyrics(id, doc, true); err != nil {
		return "歌词保存失败"
	}
	m.lyricStats = m.store.LyricsInfo()
	if cur := m.queue.Current(); cur != nil && cur.ID == id {
		m.lyricLines, m.lyricSource = lines, doc.Source
	}
	if format == "ttml" {
		return "已导入 TTML 逐字歌词"
	}
	return "已导入本地歌词"
}
