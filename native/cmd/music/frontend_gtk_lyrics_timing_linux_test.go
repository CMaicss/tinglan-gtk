//go:build linux && gtk && cgo

package main

import (
	"os"
	"path/filepath"
	"testing"

	"tinglan/native/internal/lyrics"
	"tinglan/native/internal/music"
	"tinglan/native/internal/storage"
)

func TestGTKStageTracksPlayheadIncludingBlankCuesAndBackwardSeek(t *testing.T) {
	lines := lyrics.Parse("[00:02]first\n[00:03]\n[00:05]second")
	if len(lines) != 3 {
		t.Fatalf("expected parser to retain the blank timing cue: %#v", lines)
	}
	timeline := newGTKLyricTimeline(lines)
	for _, tc := range []struct {
		position float64
		want     int
	}{{0, -1}, {2, 0}, {3, -1}, {5, 1}, {9, 1}, {2.5, 0}} {
		if got := timeline.activeRow(tc.position); got != tc.want {
			t.Errorf("active row at %.1fs = %d, want %d", tc.position, got, tc.want)
		}
	}
	m := &model{}
	m.audioState.Position = 1.8
	m.lyricOffset = .5
	if got := timeline.activeRow(m.lyricPosition()); got != 0 {
		t.Errorf("positive offset should advance the lyrics, got row %d", got)
	}
}

func TestGTKStageImportUsesPlaybackLyricCache(t *testing.T) {
	dir := t.TempDir()
	m := &model{store: storage.New(dir)}
	track := music.Track{ID: "gtk-lyric-import-test", Title: "Song"}
	m.queue.Replace([]music.Track{track}, 0)
	path := filepath.Join(dir, "song.lrc")
	text := "[00:02.30]保留歌词\n[00:05]next"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if got := gtkImportLyricFile(m, path); got != "已导入歌词" {
		t.Fatal(got)
	}
	if len(m.lyricLines) != 2 || m.lyricLines[0].Text != "保留歌词" || m.lyricSource != "本地导入" {
		t.Fatalf("import didn't update live lyrics: %#v (%s)", m.lyricLines, m.lyricSource)
	}
	saved, err := m.store.LoadLyrics(m.trackLyricsKey(track))
	if err != nil || saved.Text != text || saved.Source != m.lyricSource {
		t.Fatalf("playback cache didn't retain import: %+v, %v", saved, err)
	}
	if err := os.Truncate(path, 512*1024+1); err != nil {
		t.Fatal(err)
	}
	if got := gtkImportLyricFile(m, path); got == "已导入歌词" {
		t.Fatal("oversized lyrics accepted")
	}
	if len(m.lyricLines) != 2 || m.lyricLines[0].Text != "保留歌词" {
		t.Fatal("rejected import replaced the live lyrics")
	}
}
