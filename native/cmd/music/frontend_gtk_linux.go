//go:build linux && gtk && cgo

package main

/*
#cgo pkg-config: gtk4 libadwaita-1
#include <stdlib.h>
#include "gtk_linux.h"
*/
import "C"

import (
	_ "embed"
	"errors"
	"fmt"
	"math"
	"runtime/cgo"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/vivaui/viva/app/singleinstance"
	"tinglan/native/internal/icons"
	"tinglan/native/internal/media"
	"tinglan/native/internal/music"
)

//go:embed gtk_collection.css
var gtkCollectionCSS string

const experimentalFrontend = true

func validateFrontend(demo int, profile string, _ bool) error {
	if demo <= 0 && profile == "" {
		return errors.New("experimental GTK build requires --demo or an isolated --profile")
	}
	return nil
}

type gtkSession struct {
	model           *model
	dispatch        gtkDispatch
	handle          cgo.Handle
	generation      uint64
	count           int
	status          string
	playback        gtkPlayback
	prefs           gtkTransportPrefs
	havePrefs       bool
	volumeSave      *time.Timer
	volumeEpoch     atomic.Uint64
	volumeReady     atomic.Uint64
	volumePending   bool
	coverURL        string
	coverID         uint64
	checkTimer      *time.Timer
	checkUntil      time.Time
	checkOK         bool
	historyRevision uint64
	historyCount    int
	queueRevision   uint64
	queueRows       []gtkQueueRow
}

type gtkTransportPrefs struct {
	mode, quality int
	volume        float64
}

func transportPreferences(m *model) gtkTransportPrefs {
	p := gtkTransportPrefs{volume: m.settings.Volume}
	switch {
	case m.settings.Shuffle:
		p.mode = 3
	case m.settings.RepeatMode == "all":
		p.mode = 1
	case m.settings.RepeatMode == "one":
		p.mode = 2
	}
	if m.settings.Quality == "lossless" {
		p.quality = 1
	}
	return p
}

type gtkPlayback struct {
	title, artist      string
	playing            bool
	position, duration float64
}

func runFrontend(m *model) error {
	if !m.demo {
		claim, primary, err := singleinstance.SingleInstance("org.tinglan.unofficial.viva", nil, func([]string) { m.reopen.Store(true); m.requestUpdate() })
		if err != nil {
			m.close()
			return err
		}
		if !primary {
			m.close()
			return nil
		}
		defer claim.Close()
	}
	s := &gtkSession{model: m}
	s.handle = cgo.NewHandle(s)
	defer s.handle.Delete()
	defer s.close()
	wake := func() { s.dispatch.request(func() uint32 { return uint32(C.tl_post(C.uintptr_t(s.handle))) }) }
	m.uiWake.Store(&wake)
	css := C.CString(gtkCollectionCSS)
	defer C.free(unsafe.Pointer(css))
	if status := C.tl_run(C.uintptr_t(s.handle), css); status != 0 {
		return fmt.Errorf("GTK exited with status %d", status)
	}
	if m.backgroundCheck && !s.checkOK {
		return errors.New("hidden-window playback did not advance to the second track")
	}
	return nil
}

func gtkFrom(handle C.uintptr_t) *gtkSession { return cgo.Handle(handle).Value().(*gtkSession) }

func (s *gtkSession) close() {
	if s.volumeSave != nil {
		s.volumeSave.Stop()
	}
	if s.volumePending {
		s.model.saveSettings()
		s.volumePending = false
	}
	if s.checkTimer != nil {
		s.checkTimer.Stop()
	}
	if id := s.dispatch.stop(); id != 0 {
		C.tl_cancel(C.uint(id))
	}
	s.closeLibrary()
	s.model.close()
}

//export tlGoReady
func tlGoReady(handle C.uintptr_t) {
	s := gtkFrom(handle)
	m := s.model
	m.quit = func() { C.tl_quit() }
	if len(icons.AppPNG) != 0 {
		C.tl_set_logo(unsafe.Pointer(&icons.AppPNG[0]), C.size_t(len(icons.AppPNG)))
	}
	m.view = 2 // Reuse the search activation branch, which enqueues rather than replacing.
	if !m.demo {
		m.mediaController = media.New(func(e media.Event) {
			m.mu.Lock()
			if !m.closed {
				m.mediaEvents = append(m.mediaEvents, e)
			}
			m.mu.Unlock()
			m.requestUpdate()
		})
		m.restoreSession()
	}
	m.requestUpdate()
}

//export tlGoWake
func tlGoWake(handle C.uintptr_t) {
	s := gtkFrom(handle)
	if !s.dispatch.begin() {
		return
	}
	if s.model.reopen.Load() {
		C.tl_present()
	}
	m := s.model
	if m.backgroundCheck && s.checkUntil.IsZero() && C.tl_mapped() != 0 {
		C.tl_hide()
		s.checkUntil = time.Now().Add(12 * time.Second)
		s.checkTimer = time.AfterFunc(time.Until(s.checkUntil), m.requestUpdate)
		m.startTracks(m.tracks, 0)
	}
	if s.volumePending && s.volumeReady.Load() == s.volumeEpoch.Load() {
		s.volumePending = false
		m.saveSettings()
	}
	m.wake() // Model updates run even when the window is not mapped.
	if m.backgroundCheck && !s.checkUntil.IsZero() {
		if C.tl_mapped() == 0 && m.queue.Index == 1 && m.audioState.Playing {
			s.checkOK = true
			fmt.Println("BACKGROUND_CHECK_OK: GTK hidden window advanced to track 2")
			C.tl_quit()
		} else if !time.Now().Before(s.checkUntil) {
			C.tl_quit()
		}
	}
	if C.tl_mapped() != 0 {
		s.render()
	}
}

//export tlGoMapped
func tlGoMapped(handle C.uintptr_t) { gtkFrom(handle).model.requestUpdate() }

//export tlGoClosing
func tlGoClosing(handle C.uintptr_t) { gtkFrom(handle).close() }

//export tlGoSearch
func tlGoSearch(handle C.uintptr_t, query *C.char) {
	s := gtkFrom(handle)
	s.model.query.SetText(C.GoString(query))
	s.model.search()
	s.render()
	C.tl_reset_scroll()
}

//export tlGoAction
func tlGoAction(handle C.uintptr_t, action C.int, value C.double) {
	s := gtkFrom(handle)
	m := s.model
	switch action {
	case 1:
		m.stepPlayback(-1)
	case 2:
		m.togglePlayback()
	case 3:
		m.stepPlayback(1)
	case 4:
		m.seekPlayback(float64(value))
	case 5:
		v := float64(value)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return
		}
		m.settings.Volume = max(0, min(1, v))
		if m.player != nil && !m.demo {
			_ = m.player.SetVolume(m.settings.Volume)
		}
		if s.volumeSave != nil {
			s.volumeSave.Stop()
		}
		s.volumePending = true
		epoch := s.volumeEpoch.Add(1)
		s.volumeSave = time.AfterFunc(180*time.Millisecond, func() {
			for {
				ready := s.volumeReady.Load()
				if ready >= epoch || s.volumeReady.CompareAndSwap(ready, epoch) {
					break
				}
			}
			m.requestUpdate()
		})
	case 6:
		i := int(value)
		if i >= 0 && i < 4 {
			m.settings.Shuffle = i == 3
			m.settings.RepeatMode = [...]string{"off", "all", "one", "off"}[i]
			m.queue.SetShuffle(i == 3)
			m.saveSettings()
		}
	case 7:
		if value == 0 || value == 1 {
			m.settings.Quality = [...]string{"standard", "lossless"}[int(value)]
			m.saveSettings()
		}
	case 8:
		if m.stageOpen || m.queue.Current() != nil {
			m.stageOpen = !m.stageOpen
			if m.stageOpen {
				C.tl_show_lyrics(1)
			} else {
				C.tl_show_lyrics(0)
			}
		}
	}
	m.requestUpdate()
}

//export tlGoRow
func tlGoRow(handle C.uintptr_t, row C.uint) {
	m := gtkFrom(handle).model
	if int(row) < len(m.filtered) && m.trackAvailable(m.filtered[int(row)]) {
		m.playListTrack(m.filtered, int(row))
		m.requestUpdate()
	}
}

//export tlGoMore
func tlGoMore(handle C.uintptr_t) {
	m := gtkFrom(handle).model
	if !m.searchError {
		m.moreSearch()
		m.requestUpdate()
	}
}

//export tlGoHistoryRow
func tlGoHistoryRow(handle C.uintptr_t, row C.uint) {
	s := gtkFrom(handle)
	m := s.model
	if s.historyRevision == m.localHistoryRevision && int(row) < len(m.localHistory) && m.trackAvailable(m.localHistory[int(row)]) {
		m.playHistoryTrack(int(row))
		m.requestUpdate()
	}
}

func (s *gtkSession) renderHistory() {
	m := s.model
	if C.tl_history_visible() == 0 || s.historyRevision == m.localHistoryRevision {
		return
	}
	withGTKStrings(len(m.localHistory), func(i int) string {
		t := m.localHistory[i]
		return gtkRowText(t.Title, t.ArtistText())
	}, func(raw **C.char) { C.tl_set_history_rows(C.uint(s.historyCount), raw) })
	s.historyRevision, s.historyCount = m.localHistoryRevision, len(m.localHistory)
}

type gtkQueueRow struct{ id, title, artist string }

//export tlGoQueueAction
func tlGoQueueAction(handle C.uintptr_t, action C.int, index C.uint) {
	s := gtkFrom(handle)
	m := s.model
	if action != 3 && s.queueRevision != m.queue.Generation {
		s.renderQueue()
		return
	}
	switch action {
	case 1:
		m.selectQueueTrack(int(index))
	case 2:
		m.removeQueueTrack(int(index))
	case 3:
		m.clearQueue()
	}
	s.renderQueue()
	m.requestUpdate()
}

func (s *gtkSession) renderQueue() {
	q := &s.model.queue
	if C.tl_queue_visible() == 0 || s.queueRevision == q.Generation && len(s.queueRows) == len(q.Tracks) {
		return
	}
	rows := make([]gtkQueueRow, len(q.Tracks))
	for i, t := range q.Tracks {
		rows[i] = gtkQueueRow{t.ID, t.Title, t.ArtistText()}
	}
	prefix := 0
	for prefix < min(len(rows), len(s.queueRows)) && rows[prefix] == s.queueRows[prefix] {
		prefix++
	}
	oldEnd, newEnd := len(s.queueRows), len(rows)
	for oldEnd > prefix && newEnd > prefix && s.queueRows[oldEnd-1] == rows[newEnd-1] {
		oldEnd--
		newEnd--
	}
	if prefix != oldEnd || prefix != newEnd {
		withGTKStrings(newEnd-prefix, func(i int) string { row := rows[prefix+i]; return gtkRowText(row.title, row.artist) }, func(raw **C.char) {
			C.tl_set_queue_rows(C.uint(prefix), C.uint(oldEnd-prefix), raw)
		})
	}
	index := -1
	if q.Current() != nil {
		index = q.Index
	}
	C.tl_set_queue_index(C.int(index))
	s.queueRevision, s.queueRows = q.Generation, rows
}

func (s *gtkSession) render() {
	s.renderLibrary()
	s.renderSettings()
	s.renderLyrics()
	s.renderQueue()
	s.renderHistory()
	m := s.model
	if s.generation != m.searchGeneration || s.count != len(m.filtered) {
		start, remove := s.count, 0
		if s.generation != m.searchGeneration || s.count > len(m.filtered) {
			start, remove = 0, s.count
		}
		gtkRows(start, remove, m.filtered[start:])
		s.generation, s.count = m.searchGeneration, len(m.filtered)
	}
	status := ""
	switch {
	case m.searchLoading:
		status = "正在搜索…"
	case m.searchError && m.searchCatalog() == nil:
		status = "没有可搜索的音源。"
	case m.searchError:
		status = "搜索失败，请重试。"
	case m.searchQuery != "" && len(m.filtered) == 0:
		status = "未找到结果"
	}
	if status != s.status {
		text := C.CString(status)
		C.tl_set_status(text)
		C.free(unsafe.Pointer(text))
		s.status = status
	}
	prefs := transportPreferences(m)
	if !s.havePrefs || prefs != s.prefs {
		C.tl_set_preferences(C.int(prefs.mode), C.int(prefs.quality), C.double(prefs.volume))
		s.prefs, s.havePrefs = prefs, true
	}
	s.renderCover()
	var playback gtkPlayback
	if t := m.queue.Current(); t != nil {
		playback.title, playback.artist = t.Title, t.ArtistText()
		playback.playing = m.audioRunning
		playback.position, playback.duration = m.playbackTimes()
	}
	if playback != s.playback {
		title, artist := C.CString(playback.title), C.CString(playback.artist)
		var playing C.int
		if playback.playing {
			playing = 1
		}
		C.tl_set_playback(title, artist, playing, C.double(playback.position), C.double(playback.duration))
		C.free(unsafe.Pointer(title))
		C.free(unsafe.Pointer(artist))
		s.playback = playback
	}
}

func (s *gtkSession) renderCover() {
	m := s.model
	raw := ""
	if track := m.queue.Current(); track != nil {
		raw = track.CoverURL
	}
	bitmap, ok := m.covers[raw]
	if raw != "" && ok && bitmap.Width > 0 && len(bitmap.Pix) >= int(bitmap.Stride*bitmap.Height) {
		if s.coverURL != raw || s.coverID != bitmap.ID {
			m.touchCover(raw)
			C.tl_set_cover(unsafe.Pointer(&bitmap.Pix[0]), C.size_t(len(bitmap.Pix)), C.int(bitmap.Width), C.int(bitmap.Height), C.int(bitmap.Stride))
			s.coverURL, s.coverID = raw, bitmap.ID
		}
		return
	}
	if s.coverURL != "" {
		C.tl_set_cover(nil, 0, 0, 0, 0)
		s.coverURL, s.coverID = "", 0
	}
	if raw != "" && m.resourceAllowed(raw) {
		m.requestCover(raw)
	}
}

func gtkRowText(title, artist string) string {
	return strings.ReplaceAll(title, "\n", " ") + "\n" + strings.ReplaceAll(artist, "\n", " ")
}

func gtkRows(start, remove int, tracks []music.Track) {
	withGTKStrings(len(tracks), func(i int) string { return gtkRowText(tracks[i].Title, tracks[i].ArtistText()) }, func(raw **C.char) {
		C.tl_set_rows(C.uint(start), C.uint(remove), raw)
	})
}

func withGTKStrings(n int, value func(int) string, set func(**C.char)) {
	// GtkStringList copies strings during splice. The temporary, null-terminated
	// argv lives on the C heap, so the call does not expose Go pointers to GTK.
	raw := C.calloc(C.size_t(n+1), C.size_t(unsafe.Sizeof((*C.char)(nil))))
	defer C.free(raw)
	rows := unsafe.Slice((**C.char)(raw), n+1)
	for i := 0; i < n; i++ {
		rows[i] = C.CString(value(i))
	}
	set((**C.char)(raw))
	for _, text := range rows[:n] {
		C.free(unsafe.Pointer(text))
	}
}
