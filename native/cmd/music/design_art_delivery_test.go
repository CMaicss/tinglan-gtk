package main

import (
	"bytes"
	"encoding/json"
	"image"
	imagecolor "image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tinglan/native/internal/music"
	"tinglan/native/internal/storage"
)

func TestDesignArtDeliveryFromProfileCache(t *testing.T) {
	m := newModel()
	defer m.close()
	defer designArtClose(m)
	m.store = storage.New(t.TempDir())
	m.demo = true // exercise the real disk cache without a CDN dependency
	raw := "https://covers.dbankcdn.cn/fixture.png"
	img := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	img.Set(4, 2, imagecolor.NRGBA{R: 255, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SaveCover(raw, data.Bytes()); err != nil {
		t.Fatal(err)
	}
	m.tracks = []music.Track{{ID: "fixture", Source: "huawei", CoverURL: raw}}
	cmd := designCommand{Op: "art", Text: "Favorites", ID: "huawei:fixture", Index: 0}
	invalid := cmd
	invalid.ID = raw
	designArtCommand(m, invalid)
	s := designArtState(m).(designArtView)
	if s.Pending || s.Reply == nil || len(s.Reply.PNG) != 0 {
		t.Fatalf("invalid identity accepted: %+v", s)
	}
	designArtCommand(m, cmd)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s = designArtState(m).(designArtView)
		if s.Reply != nil && !s.Pending {
			if s.Reply.ID != cmd.ID {
				t.Fatalf("unexpected result identity %q", s.Reply.ID)
			}
			cfg, err := png.DecodeConfig(bytes.NewReader(s.Reply.PNG))
			if err != nil || cfg.Width != 4 || cfg.Height != 4 {
				t.Fatalf("expected bounded square PNG: %+v %v", cfg, err)
			}
			wire, _ := json.Marshal(s)
			if bytes.Contains(wire, []byte(raw)) {
				t.Fatal("art state exposed URL")
			}
			if designArtState(m).(designArtView).Reply != nil {
				t.Fatal("sent the image again on a plain poll")
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	if s.Reply == nil {
		t.Fatal("art delivery never completed")
	}
	// Reopen on the same URL with networking disabled.
	m2 := newModel()
	defer m2.close()
	defer designArtClose(m2)
	m2.store, m2.demo = storage.New(m.store.Dir()), true
	m2.tracks = []music.Track{{ID: "fixture", Source: "huawei", CoverURL: raw}}
	designArtCommand(m2, cmd)
	read := func() designArtView {
		for until := time.Now().Add(2 * time.Second); time.Now().Before(until); {
			state := designArtState(m2).(designArtView)
			if state.Reply != nil {
				return state
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("no cover response")
		return designArtView{}
	}
	if got := read(); len(got.Reply.PNG) == 0 {
		t.Fatal("reopening did not use the URL cache")
	}
	kept := filepath.Join(m2.store.Dir(), "covers", "unrelated.txt")
	if err := os.WriteFile(kept, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	designArtCommand(m2, designCommand{Op: "art-clear", Index: 1})
	designArtState(m2)
	if _, err := os.Stat(kept); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.store.LoadCover(raw); !os.IsNotExist(err) {
		t.Fatalf("old URL entry not removed: %v", err)
	}
	designArtCommand(m2, cmd)
	if got := read(); len(got.Reply.PNG) != 0 {
		t.Fatal("cleared cover still returned")
	}
}

func TestDesignArtLyricsPersistAndRejectStalePicker(t *testing.T) {
	m := newModel()
	defer m.close()
	defer designArtClose(m)
	m.store = storage.New(t.TempDir())
	track := music.Track{ID: "a", Source: "huawei"}
	m.queue.Replace([]music.Track{track}, 0)
	token := designArtState(m).(designArtView).LyricToken
	if token == "" {
		t.Fatal("missing lyric token")
	}
	path := filepath.Join(t.TempDir(), "song.lrc")
	if err := os.WriteFile(path, []byte("[00:01.00]fixture lyric"), 0600); err != nil {
		t.Fatal(err)
	}
	designArtCommand(m, designCommand{Op: "lyric-import", ID: token, Text: path})
	if got, err := m.store.LoadLyrics(m.trackLyricsKey(track)); err != nil || got.Source != "本地导入" || len(m.lyricLines) != 1 {
		t.Fatalf("lyrics not persisted: %+v %v", got, err)
	}
	designArtCommand(m, designCommand{Op: "lyric-offset", ID: token, Index: 1})
	if got := m.store.LyricOffset(m.trackOffsetKey(track)); got != .5 {
		t.Fatalf("offset = %v", got)
	}
	m.playbackGeneration++
	m.lyricLines = nil
	designArtCommand(m, designCommand{Op: "lyric-import", ID: token, Text: path})
	if m.lyricLines != nil {
		t.Fatal("stale chooser changed active lyrics")
	}
}
