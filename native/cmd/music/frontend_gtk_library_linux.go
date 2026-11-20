//go:build linux && gtk && cgo

package main

/*
#include <stdlib.h>
#include <stdint.h>
uintptr_t tl_library_controller(uintptr_t session);
void tl_library_show(int page);
void tl_library_rows(int id, unsigned int start, unsigned int remove, char **rows);
void tl_library_heading(int id, char *title, char *count, char *description, char *status, unsigned int flags);
void tl_library_dialog(uintptr_t callback, char *title, char *body, char *affirmative, char *entry, char **choices, int destructive);
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime/cgo"
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"tinglan/native/internal/librarydb"
	"tinglan/native/internal/music"
	"tinglan/native/internal/source"
)

// Keep these in sync with gtk_library_linux.c.
const (
	gtkLibraryFavorites = iota
	gtkLibraryPlaylists
	gtkLibraryDetail
	gtkLibraryRemote
	gtkLibraryPageCount
)
const (
	gtkLibPlay = iota + 1
	gtkLibPlayAll
	gtkLibOpen
	gtkLibBack
	gtkLibFavorite
	gtkLibAdd
	gtkLibRemove
	gtkLibCreate
	gtkLibRename
	gtkLibDelete
	gtkLibImportFavorites
	gtkLibBrowseRemote
	gtkLibImportPlaylist
	gtkLibUpdate
)
const (
	gtkLibCanWrite = 1 << iota
	gtkLibCanImport
	gtkLibCanEdit
	gtkLibCanUpdate
)

type gtkLibraryHeading struct {
	title, count, description, status string
	flags                             uint
}

type gtkLibraryPage struct {
	records     []string
	heading     gtkLibraryHeading
	initialized bool
}

type gtkLibrary struct {
	model                   *model
	pages                   [gtkLibraryPageCount]gtkLibraryPage
	favorites, detailTracks []music.Track
	playlists               []librarydb.Playlist
	remote                  []music.Playlist
	playlistPage            int
	rowFlags                uint
	detailID                string
	remoteEpoch             uint64
	remoteStatus            string
	workEpoch               uint64
	workCancel              context.CancelFunc
	workResult              chan gtkLibraryWork
	workKind                string
}

type gtkLibraryWork struct {
	playlistID string
	remote     []music.Playlist
	favorites  *sourceResult
	err        error
}

//export tlGoLibraryNew
func tlGoLibraryNew(session C.uintptr_t) C.uintptr_t {
	return C.uintptr_t(cgo.NewHandle(&gtkLibrary{model: gtkFrom(session).model, playlistPage: gtkLibraryPlaylists}))
}

//export tlGoLibraryFree
func tlGoLibraryFree(handle C.uintptr_t) {
	h := cgo.Handle(handle)
	if l, ok := h.Value().(*gtkLibrary); ok && l.workCancel != nil {
		l.workCancel()
	}
	h.Delete()
}

//export tlGoLibraryRespond
func tlGoLibraryRespond(handle C.uintptr_t, text *C.char, selected C.uint) {
	cgo.Handle(handle).Value().(func(string, uint))(C.GoString(text), uint(selected))
}

//export tlGoLibraryAction
func tlGoLibraryAction(handle C.uintptr_t, page, action C.int, index C.uint) {
	l := cgo.Handle(handle).Value().(*gtkLibrary)
	if l.model.closed {
		return
	}
	l.action(int(page), int(action), int(index))
	l.model.requestUpdate()
}

//export tlGoLibraryStop
func tlGoLibraryStop(id C.uintptr_t) {
	l := cgo.Handle(id).Value().(*gtkLibrary)
	if l.workResult != nil {
		l.workCancel()
		// Wait before the shared profile closes; the worker never waits on GTK.
		<-l.workResult
		l.workResult, l.workCancel = nil, nil
	}
}

// closeLibrary must run before model.close, including app-initiated quit.
func (s *gtkSession) closeLibrary() {
	if id := C.tl_library_controller(C.uintptr_t(s.handle)); id != 0 {
		tlGoLibraryStop(id)
	}
}

func (s *gtkSession) renderLibrary() {
	if id := C.tl_library_controller(C.uintptr_t(s.handle)); id != 0 {
		cgo.Handle(id).Value().(*gtkLibrary).render()
	}
}

func gtkLibraryPlay(m *model, tracks []music.Track, index int) {
	if index < 0 || index >= len(tracks) {
		return
	}
	m.playFailures = 0
	// Search's shared playListTrack enqueues when m.view == 2; library playback
	// always plays the selected collection in order without changing m.view.
	m.startTracks(tracks, index)
}

func gtkLibraryToggleFavorite(m *model, track music.Track) {
	row, ok := m.canonicalRow(track.ID)
	if !ok {
		return
	}
	var err error
	if row.Favorite {
		// The legacy model toggle deletes the canonical row, which can also delete
		// playlist memberships. RemoveLink changes only the favorite relation.
		if len(row.Links) == 0 {
			m.notify("无法取消收藏")
			return
		}
		link := row.Links[0]
		err = m.local.RemoveLink(link.Source, link.TrackID)
	} else {
		// Favoriting an already collected song must not forge a source binding or
		// revive a disabled one. Reuse an existing enabled identity, no matching RPC.
		added := false
		for _, link := range row.Links {
			if link.Disabled || !link.Valid() {
				continue
			}
			_, err = m.local.Add(context.Background(), link.Track(), nil, music.DefaultMatchPolicy())
			added = true
			break
		}
		if !added {
			m.notify("没有可用音源，无法加入收藏")
			return
		}
	}
	if err != nil {
		m.notify("收藏更新失败")
		return
	}
	filtered := m.filtered
	m.installLocalLibrary()
	if m.searchQuery != "" {
		m.filtered = filtered
	}
	if row.Favorite {
		m.notify("已从本地收藏移除")
	} else {
		m.notify("已加入本地收藏")
	}
}

func gtkLibrarySameSlice[T any](a, b []T) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

func gtkLibraryText(text string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "\x00", "�").Replace(strings.ToValidUTF8(text, "�"))
}

func gtkLibraryTrackRows(m *model, tracks []music.Track, flags uint) []string {
	rows := make([]string, len(tracks))
	for i, t := range tracks {
		f := flags
		if m.favoriteKnown[t.ID] && !m.favoriteOff[t.ID] {
			f |= 1
		}
		rows[i] = strings.Join([]string{gtkLibraryText(t.Title), gtkLibraryText(t.ArtistText()), durationText(t.Duration), strconv.Itoa(int(f))}, "\n")
	}
	return rows
}

func (l *gtkLibrary) rows(page int, next []string) {
	p := &l.pages[page]
	prev := p.records
	if slices.Equal(prev, next) {
		return
	}
	start, end := 0, 0
	for start < min(len(prev), len(next)) && prev[start] == next[start] {
		start++
	}
	for end < min(len(prev), len(next))-start && prev[len(prev)-1-end] == next[len(next)-1-end] {
		end++
	}
	withGTKStrings(len(next)-start-end, func(i int) string { return next[start+i] }, func(raw **C.char) {
		C.tl_library_rows(C.int(page), C.uint(start), C.uint(len(prev)-start-end), raw)
	})
	p.records = next
}

func gtkLibraryStrings(values ...string) ([]*C.char, func()) {
	raw := make([]*C.char, len(values))
	for i, value := range values {
		raw[i] = C.CString(gtkLibraryText(value))
	}
	return raw, func() {
		for _, p := range raw {
			C.free(unsafe.Pointer(p))
		}
	}
}

func (l *gtkLibrary) heading(page int, h gtkLibraryHeading) {
	p := &l.pages[page]
	if p.initialized && p.heading == h {
		return
	}
	p.heading, p.initialized = h, true
	data, free := gtkLibraryStrings(h.title, h.count, h.description, h.status)
	defer free()
	C.tl_library_heading(C.int(page), data[0], data[1], data[2], data[3], C.uint(h.flags))
}

func (l *gtkLibrary) render() {
	l.finishWork()
	m := l.model
	writable := m.local != nil && !m.demo
	canImport, canBrowse := false, false
	if writable && m.accountSourceReady() {
		if src, ok := m.primarySource(); ok {
			caps := src.Describe().Capabilities
			canImport, canBrowse = caps.Import, caps.Playlist
		}
	}
	var rowFlags uint
	var listFlags uint
	if writable {
		rowFlags = 2
		listFlags = gtkLibCanWrite
	}
	if canImport && l.workResult == nil {
		listFlags |= gtkLibCanImport
	}
	toast := ""
	if m.toastVisible() {
		toast = m.toast
	}
	playback := m.playbackError
	if toast != "" {
		playback = toast
	}
	favoriteStatus := playback
	switch {
	case m.importing, favoriteStatus == "" && m.importStatus != "":
		favoriteStatus = m.importStatus
	case favoriteStatus == "" && !writable && !m.demo:
		favoriteStatus = "本地收藏无法读取"
	case favoriteStatus == "" && len(m.tracks) == 0:
		favoriteStatus = "还没有收藏曲目"
	}
	favoritesChanged := !gtkLibrarySameSlice(l.favorites, m.tracks)
	if favoritesChanged || rowFlags != l.rowFlags {
		l.rows(gtkLibraryFavorites, gtkLibraryTrackRows(m, m.tracks, rowFlags))
		l.favorites = m.tracks
	}
	l.heading(gtkLibraryFavorites, gtkLibraryHeading{count: fmt.Sprintf("%d 首", len(m.tracks)), status: favoriteStatus, flags: listFlags})

	if !gtkLibrarySameSlice(l.playlists, m.localPlaylists) {
		rows := make([]string, len(m.localPlaylists))
		for i, p := range m.localPlaylists {
			kind := "本地"
			if !p.Local() {
				kind = sourceLabel(p.Source) + " · 只读"
			}
			rows[i] = gtkLibraryText(p.Name) + "\n" + fmt.Sprintf("%s · %d 首", gtkLibraryText(kind), p.TrackCount)
		}
		l.rows(gtkLibraryPlaylists, rows)
		l.playlists = m.localPlaylists
	}
	playlistStatus := toast
	if playlistStatus == "" && len(l.playlists) == 0 {
		playlistStatus = "还没有歌单"
	}
	pflags := listFlags & gtkLibCanWrite
	// The remote catalog is only available after the existing account is restored.
	if canBrowse && m.session != nil && l.workResult == nil {
		pflags |= gtkLibCanImport
	}
	l.heading(gtkLibraryPlaylists, gtkLibraryHeading{status: playlistStatus, flags: pflags})

	if p := m.localDetail; p != nil && l.playlistPage == gtkLibraryDetail {
		flags := rowFlags
		detailFlags := uint(0)
		if writable && p.Local() {
			flags |= 4
			detailFlags |= gtkLibCanEdit
		}
		if canBrowse && !p.Local() && l.workResult == nil && m.session != nil && p.Source == m.primarySourceID().String() {
			detailFlags |= gtkLibCanUpdate
		}
		if favoritesChanged || l.detailID != p.ID || l.rowFlags != rowFlags || !gtkLibrarySameSlice(l.detailTracks, m.localTracks) {
			l.rows(gtkLibraryDetail, gtkLibraryTrackRows(m, m.localTracks, flags))
			l.detailID, l.detailTracks = p.ID, m.localTracks
		}
		status := playback
		if status == "" && len(m.localTracks) == 0 {
			status = "此歌单没有曲目"
		}
		description := p.Description
		if !p.Local() {
			readOnly := sourceLabel(p.Source) + " · 只读副本"
			if description != "" {
				description = readOnly + " · " + description
			} else {
				description = readOnly
			}
		}
		l.heading(gtkLibraryDetail, gtkLibraryHeading{title: p.Name, count: fmt.Sprintf("%d 首", len(m.localTracks)), description: description, status: status, flags: detailFlags})
	} else if l.playlistPage == gtkLibraryDetail {
		l.show(gtkLibraryPlaylists)
	}
	l.rowFlags = rowFlags
	if l.playlistPage == gtkLibraryRemote {
		if m.session == nil || m.accountEpoch != l.remoteEpoch {
			l.remote = nil
			l.rows(gtkLibraryRemote, nil)
			l.show(gtkLibraryPlaylists)
			return
		}
		status := l.remoteStatus
		switch {
		case l.workKind != "":
			status = "正在" + l.workKind + "…"
		case toast != "":
			status = toast
		case status == "" && len(l.remote) == 0:
			status = "没有可导入的歌单"
		case status == "":
			status = "选择要导入的歌单"
		}
		l.heading(gtkLibraryRemote, gtkLibraryHeading{status: status})
	}
}

func (l *gtkLibrary) show(page int) {
	l.playlistPage = page
	C.tl_library_show(C.int(page))
}

func (l *gtkLibrary) dialog(title, body, affirmative string, entry *string, choices []string, destructive bool, apply func(string, uint)) {
	m := l.model
	callback := cgo.NewHandle(func(text string, index uint) {
		if apply != nil && !m.closed {
			apply(text, index)
			m.requestUpdate()
		}
	})
	labels, free := gtkLibraryStrings(title, body, affirmative)
	defer free()
	var field *C.char
	if entry != nil {
		field = C.CString(gtkLibraryText(*entry))
		defer C.free(unsafe.Pointer(field))
	}
	var danger C.int
	if destructive {
		danger = 1
	}
	show := func(raw **C.char) {
		C.tl_library_dialog(C.uintptr_t(callback), labels[0], labels[1], labels[2], field, raw, danger)
	}
	if len(choices) == 0 {
		show(nil)
	} else {
		withGTKStrings(len(choices), func(i int) string { return gtkLibraryText(choices[i]) }, show)
	}
}

func (l *gtkLibrary) create(track music.Track) {
	m := l.model
	empty := ""
	l.dialog("新建歌单", "", "创建", &empty, nil, false, func(text string, _ uint) {
		m.addTrack = track
		m.newPlaylist.SetText(text)
		defer func() { m.addTrack = music.Track{}; m.newPlaylist.SetText("") }()
		m.createPlaylist()
		if track.ID == "" && m.localDetail != nil {
			l.show(gtkLibraryDetail)
		}
	})
}

func (l *gtkLibrary) add(track music.Track) {
	m := l.model
	if m.local == nil {
		return
	}
	names := []string{"新建歌单…"}
	ids := []string{""}
	for _, p := range m.localPlaylists {
		if p.Local() {
			names = append(names, p.Name)
			ids = append(ids, p.ID)
		}
	}
	if len(ids) == 1 {
		l.create(track)
		return
	}
	l.dialog("添加到歌单", track.Title, "添加", nil, names, false, func(_ string, index uint) {
		if index >= uint(len(ids)) {
			return
		}
		if index == 0 {
			l.create(track)
			return
		}
		if p, ok := m.playlistByID(ids[index]); !ok || !p.Local() {
			m.notify("本地歌单不可用")
			return
		}
		m.addTrack = track
		m.addToPlaylist(ids[index])
		m.addTrack = music.Track{}
	})
}

func (l *gtkLibrary) action(page, action, index int) {
	m := l.model
	if page != gtkLibraryFavorites && page != l.playlistPage {
		return
	}
	tracks := l.favorites
	if page == gtkLibraryDetail {
		tracks = l.detailTracks
	}
	switch action {
	case gtkLibPlay:
		gtkLibraryPlay(m, tracks, index)
	case gtkLibPlayAll:
		gtkLibraryPlay(m, tracks, 0)
	case gtkLibOpen:
		if index >= 0 && index < len(l.playlists) {
			if p, ok := m.playlistByID(l.playlists[index].ID); ok {
				m.openPlaylist(p)
				l.show(gtkLibraryDetail)
			}
		}
	case gtkLibBack:
		m.localDetail, m.localTracks = nil, nil
		l.show(gtkLibraryPlaylists)
	case gtkLibFavorite, gtkLibAdd, gtkLibRemove:
		if index < 0 || index >= len(tracks) || m.local == nil {
			return
		}
		track := tracks[index]
		switch action {
		case gtkLibFavorite:
			if !m.importing {
				gtkLibraryToggleFavorite(m, track)
			} else {
				m.notify("请等待收藏导入完成")
			}
		case gtkLibAdd:
			l.add(track)
		case gtkLibRemove:
			if p := m.localDetail; p != nil && p.Local() {
				if err := m.local.RemoveFromPlaylist(context.Background(), p.ID, track.ID); err != nil {
					m.notify("从歌单移除失败")
					return
				}
				m.openPlaylist(*p)
				m.notify("已从歌单移除")
			}
		}
	case gtkLibCreate:
		l.create(music.Track{})
	case gtkLibRename:
		if p := m.localDetail; m.local != nil && p != nil && p.Local() {
			id, name := p.ID, p.Name
			l.dialog("重命名歌单", "", "保存", &name, nil, false, func(text string, _ uint) {
				p, ok := m.playlistByID(id)
				if !ok || !p.Local() || m.local == nil {
					return
				}
				text = strings.TrimSpace(text)
				if text == "" || len([]rune(text)) > 60 {
					m.notify("歌单名称须为 1–60 个字")
					return
				}
				if err := m.local.RenamePlaylist(context.Background(), id, text, p.Description); err != nil {
					m.notify("重命名失败")
					return
				}
				m.openPlaylist(p)
				m.notify("已重命名歌单")
			})
		}
	case gtkLibDelete:
		if p := m.localDetail; p != nil && p.Local() {
			id := p.ID
			l.dialog("删除歌单？", p.Name, "删除", nil, nil, true, func(_ string, _ uint) { m.deleteLocalPlaylist(id) })
		}
	case gtkLibImportFavorites:
		l.importFavorites()
	case gtkLibBrowseRemote:
		if m.local != nil && m.session != nil && m.accountSourceReady() && l.workResult == nil {
			if src, ok := m.primarySource(); ok && src.Describe().Capabilities.Playlist {
				l.remoteEpoch = m.accountEpoch
				l.remote, l.remoteStatus = nil, ""
				l.rows(gtkLibraryRemote, nil)
				l.show(gtkLibraryRemote)
				l.startWork("读取平台歌单", func(ctx context.Context) gtkLibraryWork {
					rows, err := gtkLibraryRemotePlaylists(ctx, src)
					return gtkLibraryWork{remote: rows, err: err}
				})
			}
		}
	case gtkLibImportPlaylist:
		if m.session != nil && l.remoteEpoch == m.accountEpoch && l.workResult == nil && index >= 0 && index < len(l.remote) {
			p, epoch := l.remote[index], m.accountEpoch
			if m.remoteImported(p) {
				m.notify("该歌单已导入，请从我的歌单更新本地副本")
				return
			}
			l.dialog("导入歌单", p.Name, "导入", nil, nil, false, func(_ string, _ uint) {
				if epoch == m.accountEpoch && !m.remoteImported(p) {
					l.syncPlaylist(p, librarydb.Playlist{})
				}
			})
		}
	case gtkLibUpdate:
		if p := m.localDetail; p != nil && !p.Local() && m.session != nil && l.workResult == nil {
			id, epoch := p.ID, m.accountEpoch
			l.dialog("更新本地副本？", "使用当前账号的线上歌单替换本地成员。收藏与收录音乐不受影响。", "更新", nil, nil, true, func(_ string, _ uint) {
				if m.accountEpoch != epoch {
					return
				}
				if p, ok := m.playlistByID(id); ok && !p.Local() {
					l.syncPlaylist(music.Playlist{ID: p.RemoteID, Source: music.SourceID(p.Source), Name: p.Name, Description: p.Description, CoverURL: p.CoverURL, Kind: music.PlaylistOwned}, p)
				}
			})
		}
	}
}

func (l *gtkLibrary) startWork(kind string, work func(context.Context) gtkLibraryWork) {
	m := l.model
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	ch := make(chan gtkLibraryWork, 1)
	l.workEpoch, l.workResult, l.workCancel, l.workKind = m.accountEpoch, ch, cancel, kind
	go func() {
		defer cancel()
		ch <- work(ctx)
		m.requestUpdate()
	}()
}

func (l *gtkLibrary) finishWork() {
	if l.workResult == nil {
		return
	}
	m := l.model
	if l.workEpoch != m.accountEpoch || !m.accountSourceReady() {
		l.workCancel()
	}
	select {
	case result := <-l.workResult:
		kind := l.workKind
		l.workResult, l.workCancel, l.workKind = nil, nil, ""
		if l.workEpoch != m.accountEpoch || !m.accountSourceReady() {
			m.importing = false
			m.importStatus = ""
			return
		}
		if result.favorites != nil {
			// Use the shared completion path and keep an active search intact.
			filtered := m.filtered
			m.postSource(*result.favorites)
			m.applySourceResults()
			if m.searchQuery != "" {
				m.filtered = filtered
			}
			return
		}
		if result.err != nil {
			message := friendlySourceError(result.err)
			if errors.Is(result.err, errRemoteEmpty) {
				message = "平台返回空歌单；已保留本地副本"
			}
			if kind == "读取平台歌单" {
				l.remoteStatus = kind + "失败：" + message
			}
			m.notify(kind + "失败：" + message)
			return
		}
		if kind == "读取平台歌单" {
			l.remote = result.remote
			rows := make([]string, len(l.remote))
			for i, p := range l.remote {
				rows[i] = gtkLibraryText(p.Name) + "\n" + gtkLibraryText(sourceLabel(p.Source.String()))
			}
			l.rows(gtkLibraryRemote, rows)
			return
		}
		m.refreshPlaylists()
		if m.localDetail != nil && m.localDetail.ID == result.playlistID {
			m.openPlaylist(*m.localDetail)
		}
		m.notify(kind + "完成")
	default:
	}
}

func gtkLibraryRemotePlaylists(ctx context.Context, src source.Source) ([]music.Playlist, error) {
	var rows []music.Playlist
	seen, cursors := map[string]bool{}, map[string]bool{}
	for cursor := ""; ; {
		p, err := src.Catalog().Playlists(ctx, source.PlaylistRequest{Cursor: cursor, Limit: 50})
		if err != nil {
			return nil, err
		}
		for _, row := range p.Items {
			if row.ID == "" || seen[row.ID] || (row.Source != "" && row.Source != src.ID()) {
				continue
			}
			row.Source = src.ID()
			seen[row.ID] = true
			rows = append(rows, row)
		}
		if !p.HasMore && p.Next == "" {
			return rows, nil
		}
		if p.Next == "" || cursors[p.Next] {
			return nil, errors.New("invalid playlist cursor")
		}
		cursors[p.Next] = true
		cursor = p.Next
	}
}

func (l *gtkLibrary) syncPlaylist(p music.Playlist, existing librarydb.Playlist) {
	m := l.model
	if l.workResult != nil || m.local == nil || m.session == nil || !m.accountSourceReady() {
		return
	}
	src, ok := m.primarySource()
	if !ok || src.ID() != p.Source || !src.Describe().Capabilities.Playlist {
		return
	}
	local := m.local
	account := m.session.Account()
	kind := "导入歌单"
	if existing.ID != "" {
		kind = "更新本地副本"
	}
	l.startWork(kind, func(ctx context.Context) gtkLibraryWork {
		fail := func(err error) gtkLibraryWork { return gtkLibraryWork{err: err} }
		// Verify the result still belongs to the account that confirmed the action.
		verify := func() error {
			status, err := src.Auth().Status(ctx)
			if err != nil {
				return err
			}
			if status.State != source.AuthSignedIn || status.AccountKey != account {
				return source.NewError(src.ID(), "playlist", source.KindLogin, errors.New("account changed"))
			}
			return ctx.Err()
		}
		if err := verify(); err != nil {
			return fail(err)
		}
		var tracks []music.Track
		cursors := map[string]bool{}
		for cursor := ""; ; {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			page, err := src.Library().Import(ctx, source.ImportRequest{Collection: source.Collection{Kind: "playlist", ID: p.ID, PlaylistKind: p.Kind}, Cursor: cursor, Limit: 50})
			if err != nil {
				return fail(err)
			}
			if page.Partial || page.RemoteDeleted {
				return fail(errors.New("incomplete playlist snapshot"))
			}
			for _, t := range page.Tracks {
				if t.Source != p.Source || !t.Valid() {
					return fail(errors.New("invalid playlist member"))
				}
			}
			tracks = append(tracks, page.Tracks...)
			if page.Done() {
				break
			}
			if cursors[page.Next] {
				return fail(errors.New("repeated member cursor"))
			}
			cursors[page.Next] = true
			cursor = page.Next
		}
		if len(tracks) == 0 && existing.TrackCount != 0 {
			return fail(errRemoteEmpty)
		}
		if err := verify(); err != nil {
			return fail(err)
		}
		ids := make([]string, 0, len(tracks))
		for _, t := range tracks {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			if row, ok := local.RowForLink(t.Source, t.ID); ok {
				ids = append(ids, row.ID)
				continue
			}
			id, err := local.AddToLibrary(ctx, t, nil, music.DefaultMatchPolicy())
			if err != nil {
				return fail(err)
			}
			ids = append(ids, id)
		}
		if err := verify(); err != nil {
			return fail(err)
		}
		pl := existing
		if pl.ID == "" {
			var err error
			pl, err = local.EnsureImportedPlaylist(ctx, p.Source, p.ID, p.Name, p.Description, p.CoverURL)
			if err != nil {
				return fail(err)
			}
		}
		if err := local.ReplaceImportedMembers(ctx, pl.ID, ids, pl.Name, pl.Description, pl.CoverURL); err != nil {
			if existing.ID == "" {
				cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = local.DeletePlaylist(cleanup, pl.ID)
			}
			return fail(err)
		}
		return gtkLibraryWork{playlistID: pl.ID}
	})
}

func (l *gtkLibrary) importFavorites() {
	m := l.model
	if l.workResult != nil || m.importing || m.local == nil || !m.accountSourceReady() {
		return
	}
	src, ok := m.primarySource()
	if !ok || !src.Describe().Capabilities.Import {
		return
	}
	if m.session == nil {
		l.dialog("导入收藏", "请先在设置中登录"+m.accountSourceName()+"，再导入收藏。", "", nil, nil, false, nil)
		return
	}
	epoch := m.accountEpoch
	l.dialog("导入收藏", m.accountSourceName(), "导入", nil, nil, false, func(_ string, _ uint) {
		if m.accountEpoch != epoch || m.session == nil || m.local == nil || m.importing || l.workResult != nil {
			return
		}
		collection := source.Collection{Kind: "favorites", Name: "我的收藏"}
		for _, c := range src.Describe().Capabilities.Collections {
			if c.Kind == "favorites" {
				collection = c
				break
			}
		}
		local, account := m.local, m.session.Account()
		judge, policy := m.matchJudge()
		m.importing = true
		m.importStatus = "正在导入 " + m.accountSourceName() + " 收藏…"
		l.startWork("导入收藏", func(ctx context.Context) gtkLibraryWork {
			status, err := src.Auth().Status(ctx)
			if err == nil && (status.State != source.AuthSignedIn || status.AccountKey != account) {
				err = source.NewError(src.ID(), "favorites", source.KindLogin, errors.New("account changed"))
			}
			r := sourceResult{kind: "import-done", err: err, message: src.ID().String()}
			if err == nil {
				r.result, r.err = local.Import(ctx, src, collection, judge, policy, nil)
			}
			return gtkLibraryWork{favorites: &r}
		})
		m.importCancel = l.workCancel
	})
}
