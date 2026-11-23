package main

import (
	"bytes"
	"context"
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

func TestDesignStageUpgradesFromAlbumOnlyWhenTrackIsTooSmall(t *testing.T) {
	fixture := func(n int) []byte {
		var buf bytes.Buffer
		if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, n, n))); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	for _, width := range []int{160, 720} {
		cache := storage.New(t.TempDir())
		track, cover := fixture(width), fixture(1000)
		calls := 0
		fetch := func(_ context.Context, key string) ([]byte, error) {
			if key == "track" {
				return track, nil
			}
			return cover, nil
		}
		album := func(context.Context) string { calls++; return "detail" }
		payload := designStageLoad(context.Background(), cache, []string{"track"}, fetch, album, "album:huawei:test")
		cfg, _, err := image.DecodeConfig(bytes.NewReader(payload))
		want, wantCalls := 720, 0
		if width == 160 {
			want, wantCalls = designStageSide, 1
		}
		if err != nil || cfg.Width != want || calls != wantCalls {
			t.Fatalf("track=%d: got width=%d, album calls=%d, err=%v", width, cfg.Width, calls, err)
		}
		offline := designStageLoad(context.Background(), cache, []string{"track"}, nil, nil, "album:huawei:test")
		if !bytes.Equal(offline, payload) {
			t.Fatal("stage artwork was not reusable offline")
		}
	}
}

func TestDesignStageRevalidatesSmallImageAtSameURL(t *testing.T) {
	cache := storage.New(t.TempDir())
	const raw = "https://covers.dbankcdn.cn/fixture.png"
	fixture := func(n int) []byte {
		var out bytes.Buffer
		if err := png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, n, n))); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	if err := cache.SaveCover(raw, fixture(180)); err != nil {
		t.Fatal(err)
	}
	large := fixture(640)
	calls := 0
	fetch := func(_ context.Context, key string) ([]byte, error) {
		if key != raw {
			t.Fatalf("unexpected URL %s", key)
		}
		calls++
		return large, nil
	}
	for range 2 {
		data := designStageLoad(context.Background(), cache, []string{raw}, fetch, nil, "")
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != 640 {
			t.Fatalf("width=%d, err=%v", cfg.Width, err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected one revalidation, got %d", calls)
	}
}

func TestDesignStageDoesNotReuseThumbnailPixels(t *testing.T) {
	m := newModel()
	defer m.close()
	defer designArtClose(m)
	m.store, m.demo = storage.New(t.TempDir()), true
	raw := "https://covers.dbankcdn.cn/fixture.png"
	m.queue.Replace([]music.Track{{ID: "fixture", Title: "Fixture", Source: "huawei", CoverURL: raw}}, 0)
	for i, cache := range []*storage.Store{m.store, designStageStore(m.store)} {
		width := []int{192, 640}[i]
		var data bytes.Buffer
		if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, width, width))); err != nil {
			t.Fatal(err)
		}
		if err := cache.SaveCover(raw, data.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	for _, req := range []struct {
		field string
		width int
	}{{"Queue", 192}, {"Stage", 640}} {
		designArtCommand(m, designCommand{Op: "art", Text: req.field, ID: "huawei:fixture", Index: 0})
		until := time.Now().Add(2 * time.Second)
		for {
			r := designArtState(m).(designArtView).Reply
			if r != nil {
				cfg, _, err := image.DecodeConfig(bytes.NewReader(r.PNG))
				if err != nil || cfg.Width != req.width {
					t.Fatalf("%s: width=%d, error=%v", req.field, cfg.Width, err)
				}
				break
			}
			if time.Now().After(until) {
				t.Fatal("cover did not complete")
			}
			time.Sleep(time.Millisecond)
		}
	}
}

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
