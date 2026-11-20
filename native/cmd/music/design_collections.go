package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"tinglan/native/internal/canonical"
	"tinglan/native/internal/librarydb"
	"tinglan/native/internal/music"
	"tinglan/native/internal/source"
)

// The stdio loop owns all controller fields. Workers only receive captured
// request inputs and send a single value back; closing the bridge joins them.
var designCollectionControllers sync.Map // *model -> *designCollections

type designCollections struct {
	status                        string
	notice                        uint64
	detail                        *designCollectionDetail
	serial                        uint64
	remote                        []music.Playlist
	remoteSource                  music.SourceID
	remoteEpoch, remoteGeneration uint64
	remoteAccount                 string
	work                          *designCollectionWork
}

type designCollectionWork struct {
	cancel context.CancelFunc
	done   chan designCollectionResult
	kind   string
	source music.SourceID
	epoch  uint64
	serial uint64
}

type designCollectionResult struct {
	remote            []music.Playlist
	tracks            []music.Track
	albums            []music.Album
	title, playlistID string
	favorites         *sourceResult
	err               error
}

type designCollectionDetail struct {
	ID, Title, Kind, Subtitle, Source string
	Serial                            uint64
	Loading                           bool
	Albums                            []designCollectionChoice
}
type designCollectionChoice struct{ ID, Name string }
type designCollectionRemoteChoice struct {
	ID, Name string
	Imported bool
}
type designCollectionSource struct {
	ID, Name             string
	Favorites, Playlists bool
}
type designCollectionView struct {
	CanEdit, Busy                          bool
	Status                                 string
	Notice, AccountEpoch, RemoteGeneration uint64
	RemoteSource                           string
	Detail                                 *designCollectionDetail
	Remote                                 []designCollectionRemoteChoice
	Sources                                []designCollectionSource
	FavoriteIDs                            map[string]bool
}

func designCollectionController(m *model) *designCollections {
	if v, ok := designCollectionControllers.Load(m); ok {
		return v.(*designCollections)
	}
	cs := &designCollections{}
	designCollectionControllers.Store(m, cs)
	return cs
}

func designCollectionClose(m *model) {
	if v, ok := designCollectionControllers.LoadAndDelete(m); ok {
		if work := v.(*designCollections).work; work != nil {
			work.cancel()
			<-work.done
		}
	}
}

func (cs *designCollections) message(text string) {
	cs.status = text
	cs.notice++
}

func (cs *designCollections) start(m *model, kind string, id music.SourceID, fn func(context.Context) designCollectionResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	w := &designCollectionWork{cancel: cancel, done: make(chan designCollectionResult, 1), kind: kind, source: id, epoch: m.accountEpoch, serial: cs.serial}
	cs.work = w
	cs.message("正在" + kind + "…")
	go func() { defer cancel(); w.done <- fn(ctx) }()
}

func (cs *designCollections) finish(m *model) {
	w := cs.work
	if w == nil {
		return
	}
	stale := w.epoch != m.accountEpoch || !m.sourceEnabled(w.source)
	if stale {
		w.cancel()
	}
	select {
	case r := <-w.done:
		cs.work = nil
		if w.kind == "读取详情" {
			if cs.detail == nil || cs.detail.Serial != w.serial {
				return
			}
			cs.detail.Loading = false
			if !stale && r.err == nil {
				if r.title != "" {
					cs.detail.Title = r.title
				}
				m.localTracks = r.tracks
				for _, album := range r.albums {
					if album.ID != "" {
						cs.detail.Albums = append(cs.detail.Albums, designCollectionChoice{album.ID, album.Title})
					}
				}
				cs.status = ""
				return
			}
		}
		if stale {
			if w.kind == "导入收藏" {
				m.importing = false
				m.importStatus = ""
			}
			cs.message("音源或账号已变化，请重新操作")
			return
		}
		if r.favorites != nil {
			filtered := m.filtered
			m.postSource(*r.favorites)
			m.applySourceResults()
			if m.searchQuery != "" {
				m.filtered = filtered
			}
			cs.message(m.importStatus)
			return
		}
		if r.err != nil {
			if errors.Is(r.err, errRemoteEmpty) {
				cs.message("平台返回空歌单；已保留本地副本")
			} else {
				cs.message(w.kind + "失败：" + friendlySourceError(r.err))
			}
			return
		}
		if w.kind == "读取平台歌单" {
			cs.remote, cs.remoteSource, cs.remoteEpoch = r.remote, w.source, w.epoch
			cs.remoteGeneration++
			cs.message(fmt.Sprintf("找到 %d 个平台歌单", len(r.remote)))
			return
		}
		m.refreshPlaylists()
		if m.localDetail != nil && m.localDetail.ID == r.playlistID {
			m.openPlaylist(*m.localDetail)
		}
		cs.message(w.kind + "完成")
	default:
	}
}

// Call before copying m.localTracks into the base snapshot. The drain may
// publish a new set of detail tracks from an asynchronous request.
func designCollectionState(m *model) any {
	cs := designCollectionController(m)
	cs.finish(m)
	out := designCollectionView{CanEdit: m.local != nil, Busy: cs.work != nil, Status: cs.status, Notice: cs.notice, AccountEpoch: m.accountEpoch, RemoteGeneration: cs.remoteGeneration, FavoriteIDs: make(map[string]bool)}
	if cs.detail != nil {
		detail := *cs.detail
		if p := m.localDetail; p != nil && detail.ID == p.ID {
			detail.Title = p.Name
			detail.Subtitle = p.Description
			detail.Source = p.Source
		}
		out.Detail = &detail
	}
	if m.local != nil {
		for _, row := range m.rows {
			if row.Favorite {
				out.FavoriteIDs[":"+row.ID] = true
				for _, link := range row.Links {
					out.FavoriteIDs[link.Source.String()+":"+link.TrackID] = true
				}
			}
		}
		for _, src := range m.dialogSources() {
			caps := src.Describe().Capabilities
			// Do not make a plugin RPC on every poll. The account source's
			// session is already mirrored by the model; other sources are
			// checked when the user confirms an import.
			if caps.Import && (src.ID() != m.accountSourceID() || m.session != nil) {
				out.Sources = append(out.Sources, designCollectionSource{src.ID().String(), m.sourceDisplayName(src.ID()), caps.Import, caps.Import && caps.Playlist})
			}
		}
	}
	if cs.remoteEpoch == m.accountEpoch && m.sourceEnabled(cs.remoteSource) {
		out.RemoteSource = cs.remoteSource.String()
		imported := make(map[string]bool)
		for _, p := range m.localPlaylists {
			if !p.Local() && p.Source == out.RemoteSource {
				imported[p.RemoteID] = true
			}
		}
		for _, p := range cs.remote {
			out.Remote = append(out.Remote, designCollectionRemoteChoice{p.ID, p.Name, imported[p.ID]})
		}
	}
	return out
}

func (cs *designCollections) show(id, title, kind, subtitle string) {
	cs.serial++
	cs.detail = &designCollectionDetail{ID: id, Title: title, Kind: kind, Subtitle: subtitle, Serial: cs.serial}
}

func (cs *designCollections) open(m *model, p librarydb.Playlist) {
	m.openPlaylist(p)
	kind := "imported"
	if p.Local() {
		kind = "local"
	}
	cs.show(p.ID, p.Name, kind, p.Description)
}

func designCollectionTrack(m *model, field string, c designCommand) (music.Track, bool) {
	var rows []music.Track
	switch field {
	case "Favorites":
		rows = m.tracks
	case "Results":
		rows = m.filtered
	case "History":
		rows = m.localHistory
	case "Detail":
		rows = m.localTracks
	case "Queue":
		rows = m.queue.Tracks
	}
	if c.Index >= 0 && c.Index < len(rows) {
		t := rows[c.Index]
		if c.ID == t.Source.String()+":"+t.ID && t.ID != "" {
			return t, true
		}
	}
	return music.Track{}, false
}

func designCollectionCanonical(m *model, t music.Track) (music.CanonicalTrack, bool) {
	if t.Collected || t.Source == "" {
		if row, ok := m.canonicalRow(t.ID); ok {
			return row, true
		}
	}
	return m.local.RowForLink(t.Source, t.ID)
}

func designCollectionRefresh(m *model) {
	filtered := m.filtered
	m.installLocalLibrary()
	if m.searchQuery != "" {
		m.filtered = filtered
	}
}

func (cs *designCollections) editTrack(m *model, op, playlist string, t music.Track) {
	ctx := context.Background()
	row, collected := designCollectionCanonical(m, t)
	p, exists := m.playlistByID(playlist)
	switch op {
	case "favorite":
		if collected && row.Favorite {
			if len(row.Links) == 0 || m.local.RemoveLink(row.Links[0].Source, row.Links[0].TrackID) != nil {
				cs.message("取消收藏失败")
				return
			}
			cs.message("已取消收藏")
		} else {
			if collected {
				t = music.Track{}
				for _, link := range row.Links {
					if !link.Disabled && link.Valid() {
						t = link.Track()
						break
					}
				}
			}
			if !t.Valid() {
				cs.message("没有可用音源，无法加入收藏")
				return
			}
			if _, err := m.local.Add(ctx, t, nil, music.DefaultMatchPolicy()); err != nil {
				cs.message("收藏失败")
				return
			}
			cs.message("已加入本地收藏")
		}
	case "add", "remove":
		if !exists || !p.Local() {
			cs.message("此歌单不可编辑")
			return
		}
		if op == "remove" {
			if !collected || m.localDetail == nil || m.localDetail.ID != p.ID {
				cs.message("歌单已变化，请重新选择曲目")
				return
			}
			if err := m.local.RemoveFromPlaylist(ctx, p.ID, row.ID); err != nil {
				cs.message("移除曲目失败")
				return
			}
			cs.message("已从歌单移除")
		} else {
			id := row.ID
			if !collected {
				var err error
				id, err = m.local.AddToLibrary(ctx, t, nil, music.DefaultMatchPolicy())
				if err != nil {
					cs.message("收录曲目失败")
					return
				}
			}
			if err := m.local.AddToPlaylist(ctx, p.ID, id); err != nil {
				cs.message("加入歌单失败")
				return
			}
			cs.message("已加入歌单")
		}
		m.refreshPlaylists()
		if m.localDetail != nil && m.localDetail.ID == p.ID {
			m.openPlaylist(p)
		}
	}
	designCollectionRefresh(m)
}

func designCollectionCommand(m *model, c designCommand) bool {
	if !strings.HasPrefix(c.Op, "collection-") {
		return false
	}
	cs := designCollectionController(m)
	cs.finish(m)
	ctx := context.Background()
	op := strings.TrimPrefix(c.Op, "collection-")
	switch op {
	case "open":
		if p, ok := m.playlistByID(c.ID); ok {
			cs.open(m, p)
		} else {
			cs.message("歌单已不存在")
		}
	case "create", "rename", "delete":
		if m.local == nil {
			cs.message("本地曲库不可用")
			break
		}
		name := strings.TrimSpace(c.Text)
		if op != "delete" && (name == "" || len([]rune(name)) > 60) {
			cs.message("歌单名称须为 1–60 个字")
			break
		}
		if op == "create" {
			p, err := m.local.CreatePlaylist(ctx, name, "")
			if err != nil {
				cs.message("新建歌单失败")
				break
			}
			cs.open(m, p)
			cs.message("已新建歌单")
			break
		}
		p, ok := m.playlistByID(c.ID)
		if !ok || !p.Local() {
			cs.message("此歌单不可编辑")
			break
		}
		active := m.localDetail != nil && m.localDetail.ID == p.ID
		if op == "rename" {
			if err := m.local.RenamePlaylist(ctx, p.ID, name, p.Description); err != nil {
				cs.message("重命名歌单失败")
				break
			}
			m.refreshPlaylists()
			if active {
				m.openPlaylist(p)
			}
			cs.message("已重命名歌单")
		} else {
			if err := m.local.DeletePlaylist(ctx, p.ID); err != nil {
				cs.message("删除歌单失败")
				break
			}
			m.refreshPlaylists()
			if active {
				m.localDetail, m.localTracks, cs.detail = nil, nil, nil
			}
			cs.message("已删除歌单")
		}
	case "browse", "import", "update", "import-favorites":
		cs.importAction(m, op, c)
	case "artist-album":
		if cs.work == nil && cs.detail != nil && cs.detail.Kind == "artist" && c.Value == float64(cs.detail.Serial) && m.registry != nil {
			id := music.SourceID(cs.detail.Source)
			if src, ok := m.registry.Source(id); ok && m.sourceEnabled(id) {
				for _, album := range cs.detail.Albums {
					if album.ID == c.ID && src.Catalog() != nil {
						cs.openEntity(m, src.Catalog(), source.EntityRef{Source: id, Kind: "album", ID: album.ID}, album.Name)
						break
					}
				}
			}
		}
	default:
		action, field, ok := strings.Cut(op, "-")
		if !ok {
			break
		}
		t, valid := designCollectionTrack(m, field, c)
		if !valid {
			cs.message("曲目列表已变化，请重新选择")
			break
		}
		switch action {
		case "title", "album", "artist":
			cs.trackDetail(m, action, c.Text, t)
		case "favorite", "add", "remove":
			if m.local == nil {
				cs.message("本地曲库不可用")
				break
			}
			if m.importing && action == "favorite" {
				cs.message("请等待收藏导入完成")
				break
			}
			cs.editTrack(m, action, c.Text, t)
		}
	}
	return true
}

func (cs *designCollections) trackDetail(m *model, kind, artist string, t music.Track) {
	if kind == "title" {
		cs.show(t.Source.String()+":"+t.ID, t.Title, "track", t.ArtistText())
		m.localDetail, m.localTracks = nil, []music.Track{t}
		return
	}
	if cs.work != nil {
		cs.message("请等待当前操作完成")
		return
	}
	candidates := []music.Track{t}
	if m.local != nil {
		if row, ok := designCollectionCanonical(m, t); ok {
			for _, link := range row.Links {
				if !link.Disabled && (t.OnlySource == "" || t.OnlySource == link.Source) {
					candidates = append(candidates, link.Track())
				}
			}
		}
	}
	for _, track := range candidates {
		if m.registry == nil || !m.sourceEnabled(track.Source) {
			continue
		}
		src, ok := m.registry.Source(track.Source)
		if !ok || src.Catalog() == nil {
			continue
		}
		ref, title := source.EntityRef{Source: track.Source, Kind: kind}, ""
		if kind == "album" && track.Album != nil {
			ref.ID, title = track.Album.ID, track.Album.Title
		}
		if kind == "artist" {
			for _, a := range track.Artists {
				if (artist == "" || a.Name == artist) && a.ID != "" {
					ref.ID, title = a.ID, a.Name
					break
				}
			}
		}
		if ref.ID == "" {
			continue
		}
		cs.openEntity(m, src.Catalog(), ref, title)
		return
	}
	cs.message("该曲目没有可用的" + map[string]string{"artist": "艺人", "album": "专辑"}[kind] + "详情")
}

func (cs *designCollections) openEntity(m *model, catalog source.CatalogSource, ref source.EntityRef, title string) {
	cs.show(ref.Source.String()+":"+ref.ID, title, ref.Kind, "")
	cs.detail.Loading, cs.detail.Source = true, ref.Source.String()
	m.localDetail, m.localTracks = nil, nil
	cs.start(m, "读取详情", ref.Source, func(ctx context.Context) designCollectionResult { return designCollectionReadEntity(ctx, catalog, ref) })
}

func designCollectionReadEntity(ctx context.Context, catalog source.CatalogSource, ref source.EntityRef) (r designCollectionResult) {
	req := source.EntityRequest{Ref: ref, Limit: 50}
	seen := map[string]bool{}
	for {
		next, more := "", false
		if ref.Kind == "artist" {
			req.Section = "tracks"
			page, err := catalog.ArtistDetail(ctx, req)
			if err != nil {
				return designCollectionResult{err: err}
			}
			if req.Cursor == "" {
				r.title, r.albums = page.Artist.Name, page.Albums
			}
			r.tracks = append(r.tracks, page.Tracks...)
			next, more = page.NextTracks, page.HasMoreTracks
		} else {
			page, err := catalog.AlbumDetail(ctx, req)
			if err != nil {
				return designCollectionResult{err: err}
			}
			if req.Cursor == "" {
				r.title = page.Album.Title
			}
			r.tracks = append(r.tracks, page.Tracks...)
			next, more = page.Next, page.HasMore
		}
		if !more {
			return r
		}
		if next == "" || seen[next] {
			return designCollectionResult{err: errors.New("invalid entity cursor")}
		}
		seen[next] = true
		req.Cursor = next
	}
}

func designCollectionAuth(ctx context.Context, src source.Source, account string) error {
	st, err := src.Auth().Status(ctx)
	if err != nil {
		return err
	}
	if st.State != source.AuthSignedIn || st.AccountKey != account {
		return source.NewError(src.ID(), "collection", source.KindLogin, errors.New("account changed"))
	}
	return ctx.Err()
}

func (cs *designCollections) importAction(m *model, op string, c designCommand) {
	if m.local == nil || m.registry == nil {
		cs.message("本地曲库或音源不可用")
		return
	}
	if cs.work != nil || m.importing {
		cs.message("请等待当前操作完成")
		return
	}
	id := music.SourceID(c.ID)
	var remote music.Playlist
	var existing librarydb.Playlist
	switch op {
	case "import":
		id = cs.remoteSource
		if c.Value != float64(cs.remoteGeneration) || cs.remoteEpoch != m.accountEpoch || c.Text != id.String() || c.Index < 0 || c.Index >= len(cs.remote) || cs.remote[c.Index].ID != c.ID {
			cs.message("平台歌单列表已变化，请重新选择")
			return
		}
		remote = cs.remote[c.Index]
	case "update":
		p, ok := m.playlistByID(c.ID)
		if !ok || p.Local() {
			cs.message("本地副本已不存在")
			return
		}
		id, existing = music.SourceID(p.Source), p
	}
	src, ok := m.registry.Source(id)
	if !ok || !m.sourceEnabled(id) {
		cs.message("音源不可用")
		return
	}
	caps := src.Describe().Capabilities
	if !caps.Import || (op != "import-favorites" && !caps.Playlist) || src.Library() == nil {
		cs.message("此音源不支持该导入操作")
		return
	}
	authCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	st, err := src.Auth().Status(authCtx)
	cancel()
	if err != nil || st.State != source.AuthSignedIn {
		cs.message("请先登录" + m.sourceDisplayName(id))
		return
	}
	// Browse/confirmation is tied to a specific account epoch, not just a source.
	if op != "import" && c.Value != float64(m.accountEpoch) {
		cs.message("账号已变化，请重新确认")
		return
	}
	account, local := st.AccountKey, m.local
	if op == "import" && account != cs.remoteAccount {
		cs.message("账号已变化，请重新浏览平台歌单")
		return
	}
	switch op {
	case "browse":
		cs.remote = nil
		cs.remoteAccount = account
		cs.start(m, "读取平台歌单", id, func(ctx context.Context) designCollectionResult {
			rows, err := designCollectionRemote(ctx, src)
			if err == nil {
				err = designCollectionAuth(ctx, src, account)
			}
			return designCollectionResult{remote: rows, err: err}
		})
	case "import-favorites":
		col := source.Collection{Kind: "favorites", Name: "我的收藏"}
		for _, c := range caps.Collections {
			if c.Kind == "favorites" {
				col = c
				break
			}
		}
		m.importing = true
		judge, policy := m.matchJudge()
		cs.start(m, "导入收藏", id, func(ctx context.Context) designCollectionResult {
			r := sourceResult{kind: "import-done", message: id.String(), err: designCollectionAuth(ctx, src, account)}
			if r.err == nil {
				r.result, r.err = local.Import(ctx, src, col, judge, policy, nil)
			}
			return designCollectionResult{favorites: &r}
		})
	case "import", "update":
		kind := "导入歌单"
		if op == "update" {
			kind = "更新本地副本"
		}
		cs.start(m, kind, id, func(ctx context.Context) designCollectionResult {
			if existing.ID != "" {
				rows, err := designCollectionRemote(ctx, src)
				if err != nil {
					return designCollectionResult{err: err}
				}
				for _, row := range rows {
					if row.ID == existing.RemoteID {
						remote = row
						break
					}
				}
				if remote.ID == "" {
					return designCollectionResult{err: errors.New("no matching playlist in account")}
				}
			}
			pl, err := designCollectionSync(ctx, local, src, account, remote, existing)
			return designCollectionResult{playlistID: pl, err: err}
		})
	}
}

func designCollectionRemote(ctx context.Context, src source.Source) ([]music.Playlist, error) {
	if src.Catalog() == nil {
		return nil, errNoSource
	}
	req := source.PlaylistRequest{Limit: 50}
	var rows []music.Playlist
	ids, cursors := map[string]bool{}, map[string]bool{}
	for {
		page, err := src.Catalog().Playlists(ctx, req)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Items {
			if row.ID == "" || ids[row.ID] || (row.Source != "" && row.Source != src.ID()) {
				continue
			}
			row.Source = src.ID()
			ids[row.ID] = true
			rows = append(rows, row)
		}
		if !page.HasMore && page.Next == "" {
			return rows, nil
		}
		if page.Next == "" || cursors[page.Next] {
			return nil, errors.New("invalid playlist cursor")
		}
		cursors[page.Next], req.Cursor = true, page.Next
	}
}

func designCollectionSync(ctx context.Context, local *canonical.Library, src source.Source, account string, p music.Playlist, existing librarydb.Playlist) (string, error) {
	verify := func() error { return designCollectionAuth(ctx, src, account) }
	if err := verify(); err != nil {
		return "", err
	}
	if existing.ID == "" {
		_, exists, err := local.ImportedPlaylist(ctx, p.Source, p.ID)
		if err != nil {
			return "", err
		}
		if exists {
			return "", errors.New("playlist already imported; update its local copy")
		}
	}
	limit := 50
	if max := src.Describe().Capabilities.MaxPageSize; max > 0 && max < limit {
		limit = max
	}
	req := source.ImportRequest{Collection: source.Collection{Kind: "playlist", ID: p.ID, PlaylistKind: p.Kind}, Limit: limit}
	var tracks []music.Track
	cursors := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		page, err := src.Library().Import(ctx, req)
		if err != nil {
			return "", err
		}
		if page.Partial || page.RemoteDeleted {
			return "", errors.New("incomplete playlist snapshot")
		}
		for _, t := range page.Tracks {
			if t.Source != p.Source || !t.Valid() {
				return "", errors.New("invalid playlist track")
			}
		}
		tracks = append(tracks, page.Tracks...)
		if page.Done() {
			break
		}
		if cursors[page.Next] {
			return "", errors.New("repeated track cursor")
		}
		cursors[page.Next], req.Cursor = true, page.Next
	}
	if len(tracks) == 0 && existing.TrackCount != 0 {
		return "", errRemoteEmpty
	}
	if err := verify(); err != nil {
		return "", err
	}
	ids := make([]string, 0, len(tracks))
	for _, t := range tracks {
		if row, ok := local.RowForLink(t.Source, t.ID); ok {
			ids = append(ids, row.ID)
			continue
		}
		id, err := local.AddToLibrary(ctx, t, nil, music.DefaultMatchPolicy())
		if err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := verify(); err != nil {
		return "", err
	}
	pl := existing
	fresh := pl.ID == ""
	if fresh {
		var err error
		pl, err = local.EnsureImportedPlaylist(ctx, p.Source, p.ID, p.Name, p.Description, p.CoverURL)
		if err != nil {
			return "", err
		}
	}
	if err := local.ReplaceImportedMembers(ctx, pl.ID, ids, pl.Name, pl.Description, pl.CoverURL); err != nil {
		if fresh {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = local.DeletePlaylist(cleanup, pl.ID)
		}
		return "", err
	}
	return pl.ID, nil
}
