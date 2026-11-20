//go:build linux && gtk && cgo

package main

/*
#include <stdint.h>
#include <stdlib.h>

uintptr_t tl_lyrics_mapped_state(void);
void tl_lyrics_track(int present, int can_import, const char *title, const char *artist, const char *offset);
void tl_lyrics_rows(const char *const *titles, const char *const *subtitles);
void tl_lyrics_active(int row);
void tl_lyrics_cover(const void *pixels, int width, int height, size_t stride, size_t size);
void tl_lyrics_feedback(const char *text);
*/
import "C"

import (
	"fmt"
	"io"
	"os"
	"runtime/cgo"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"github.com/vivaui/viva/ui"
	"tinglan/native/internal/lyrics"
	"tinglan/native/internal/storage"
)

type gtkLyricRow struct{ text, subtitles string }

type gtkLyricCue struct {
	seconds float64
	row     int
}

type gtkLyricTimeline struct {
	rows []gtkLyricRow
	cues []gtkLyricCue
}

func newGTKLyricTimeline(lines []lyrics.Line) gtkLyricTimeline {
	var timeline gtkLyricTimeline
	for _, line := range lines {
		row := -1
		if strings.TrimSpace(line.Text) != "" {
			row = len(timeline.rows)
			timeline.rows = append(timeline.rows, gtkLyricRow{
				strings.ToValidUTF8(strings.ReplaceAll(line.Text, "\x00", ""), "�"),
				strings.ToValidUTF8(strings.ReplaceAll(strings.Join(line.Subtitles(), "\n"), "\x00", ""), "�"),
			})
		}
		if line.Time >= 0 {
			// Blank timed lines end the previous lyric without rendering a row.
			timeline.cues = append(timeline.cues, gtkLyricCue{line.Time, row})
		}
	}
	sort.SliceStable(timeline.cues, func(i, j int) bool { return timeline.cues[i].seconds < timeline.cues[j].seconds })
	return timeline
}

func (t gtkLyricTimeline) activeRow(position float64) int {
	next := sort.Search(len(t.cues), func(i int) bool { return position < t.cues[i].seconds })
	if next == 0 {
		return -1
	}
	return t.cues[next-1].row
}

type gtkLyricTrack struct {
	present, canImport        bool
	id, title, artist, offset string
}

type gtkLyricsState struct {
	session      cgo.Handle
	track        gtkLyricTrack
	lines        []lyrics.Line
	timeline     gtkLyricTimeline
	active       int
	artwork      ui.Canvas
	coverID      uint64
	accountEpoch uint64
	importTrack  string
	importEpoch  uint64
	importGen    uint64
}

// The widget owns this handle; the state does not keep a GTK pointer alive.
//
//export tlGoLyricsNew
func tlGoLyricsNew(session C.uintptr_t) C.uintptr_t {
	return C.uintptr_t(cgo.NewHandle(&gtkLyricsState{session: cgo.Handle(session), active: -1}))
}

//export tlGoLyricsFree
func tlGoLyricsFree(state C.uintptr_t) { cgo.Handle(state).Delete() }

//export tlGoLyricsMapped
func tlGoLyricsMapped(handle C.uintptr_t) { gtkLyricState(handle).model().requestUpdate() }

func gtkLyricState(state C.uintptr_t) *gtkLyricsState {
	return cgo.Handle(state).Value().(*gtkLyricsState)
}

func gtkLyricsString(text string, f func(*C.char)) {
	value := C.CString(text)
	defer C.free(unsafe.Pointer(value))
	f(value)
}

func (s *gtkSession) renderLyrics() {
	if !s.model.stageOpen {
		return
	}
	handle := C.tl_lyrics_mapped_state()
	if handle == 0 {
		return
	}
	state := gtkLyricState(handle)
	m := s.model
	var view gtkLyricTrack
	var bitmap ui.Bitmap
	var lines []lyrics.Line
	if track := m.queue.Current(); track != nil {
		view = gtkLyricTrack{true, !m.resolving, track.ID, track.Title, track.ArtistText(), fmt.Sprintf("%+.1f 秒", m.lyricOffset)}
		lines = m.lyricLines
		// Use the shared cover resolver, retries, allowlist and cache. Capture
		// its bitmap for GTK without starting a second resource download path.
		state.artwork.Reset()
		if m.trackCover(&state.artwork, *track, rect(0, 0, 64, 64)) {
			bitmap = state.artwork.Images()[0].Bitmap
		}
		state.artwork.Reset()
	}
	if view != state.track {
		if view.id != state.track.id {
			C.tl_lyrics_feedback(nil)
		}
		gtkLyricsString(view.title, func(title *C.char) {
			gtkLyricsString(view.artist, func(artist *C.char) {
				gtkLyricsString(view.offset, func(offset *C.char) {
					C.tl_lyrics_track(gtkLyricsBool(view.present), gtkLyricsBool(view.canImport), title, artist, offset)
				})
			})
		})
		state.track = view
	}
	if bitmap.ID != state.coverID || m.accountEpoch != state.accountEpoch {
		if len(bitmap.Pix) > 0 {
			C.tl_lyrics_cover(unsafe.Pointer(&bitmap.Pix[0]), C.int(bitmap.Width), C.int(bitmap.Height), C.size_t(bitmap.Stride), C.size_t(len(bitmap.Pix)))
		} else {
			C.tl_lyrics_cover(nil, 0, 0, 0, 0)
		}
		state.coverID, state.accountEpoch = bitmap.ID, m.accountEpoch
	}
	// The model replaces (rather than mutates) the parsed slice on each load.
	// Retaining that slice makes the identity check safe across allocations.
	if len(lines) != len(state.lines) || len(lines) > 0 && &lines[0] != &state.lines[0] {
		state.lines = lines
		state.timeline = newGTKLyricTimeline(lines)
		rows := state.timeline.rows
		withGTKStrings(len(rows), func(i int) string { return rows[i].text }, func(titles **C.char) {
			withGTKStrings(len(rows), func(i int) string { return rows[i].subtitles }, func(subtitles **C.char) {
				C.tl_lyrics_rows(titles, subtitles)
			})
		})
		state.active = -1
	}
	if row := state.timeline.activeRow(m.lyricPosition()); row != state.active {
		C.tl_lyrics_active(C.int(row))
		state.active = row
	}
}

func gtkLyricsBool(value bool) C.int {
	if value {
		return 1
	}
	return 0
}

func (state *gtkLyricsState) model() *model { return state.session.Value().(*gtkSession).model }

//export tlGoLyricsOffset
func tlGoLyricsOffset(handle C.uintptr_t, direction C.int) {
	m := gtkLyricState(handle).model()
	if m.queue.Current() != nil && m.stageOpen {
		m.lyricOffset = max(-10, min(10, m.lyricOffset+float64(direction)*.5))
		m.saveLyricOffset()
		m.requestUpdate()
	}
}

//export tlGoLyricsBeginImport
func tlGoLyricsBeginImport(handle C.uintptr_t) C.int {
	state := gtkLyricState(handle)
	m := state.model()
	track := m.queue.Current()
	if track == nil || m.resolving || !m.stageOpen {
		return 0
	}
	state.importTrack, state.importEpoch, state.importGen = track.ID, m.accountEpoch, m.playbackGeneration
	return 1
}

//export tlGoLyricsFinishImport
func tlGoLyricsFinishImport(handle C.uintptr_t, path *C.char) {
	state := gtkLyricState(handle)
	m := state.model()
	track := m.queue.Current()
	if track == nil || track.ID != state.importTrack || m.accountEpoch != state.importEpoch || m.playbackGeneration != state.importGen || m.resolving || !m.stageOpen {
		return
	}
	message := "请选择本地歌词文件"
	if path != nil {
		message = gtkImportLyricFile(m, C.GoString(path))
	}
	gtkLyricsString(message, func(text *C.char) { C.tl_lyrics_feedback(text) })
	m.requestUpdate()
}

func gtkImportLyricFile(m *model, path string) string {
	const limit = 512 * 1024
	track := m.queue.Current()
	if track == nil {
		return "没有正在播放的曲目"
	}
	// Non-blocking open prevents special files such as FIFOs from hanging GTK.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "无法读取歌词文件"
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return "歌词文件不可用或超过 512KB"
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) > limit {
		return "歌词文件不可用或超过 512KB"
	}
	text, err := lyrics.Decode(data)
	if err != nil {
		return "歌词编码无法识别"
	}
	lines, _ := lyrics.ParseAny(text)
	if len(newGTKLyricTimeline(lines).rows) == 0 {
		return "歌词文件没有可用内容"
	}
	const source = "本地导入"
	if m.store != nil {
		if err := m.store.SaveLyrics(m.trackLyricsKey(*track), storage.LyricDocument{Text: text, Source: source}, true); err != nil {
			return "歌词保存失败"
		}
	}
	m.lyricLines, m.lyricSource = lines, source
	return "已导入歌词"
}
