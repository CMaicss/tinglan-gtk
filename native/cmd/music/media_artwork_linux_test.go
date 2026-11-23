package main

import (
	"image"
	"image/png"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"tinglan/native/internal/media"
	"tinglan/native/internal/music"
	"tinglan/native/internal/storage"
)

func TestMediaArtworkExportsCachedPNGAndRejectsLateTrack(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	m := newModel()
	defer m.close()
	m.store, m.demo = storage.New(t.TempDir()), true
	raw := "https://covers.dbankcdn.cn/fixture.png"
	data := designArtPNG(image.NewNRGBA(image.Rect(0, 0, 16, 16)))
	if err := m.store.SaveCover(raw, data); err != nil {
		t.Fatal(err)
	}
	tracks := []music.Track{{ID: "first", Source: "huawei", CoverURL: raw}, {ID: "second", Source: "huawei"}}
	m.queue.Replace(tracks, 0)
	if got := m.updateMediaArtwork(); got != "" {
		t.Fatalf("art before completion: %q", got)
	}
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for len(m.mediaArt.done) == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if len(m.mediaArt.done) == 0 {
			t.Fatal("artwork did not load")
		}
	}
	wait()
	m.queue.Replace(tracks, 1)
	if got := m.updateMediaArtwork(); got != "" {
		t.Fatalf("art from previous track: %q", got)
	}
	m.queue.Replace(tracks, 0)
	m.updateMediaArtwork()
	wait()
	uri := m.updateMediaArtwork()
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" || parsed.Host != "" {
		t.Fatalf("expected a local file URI, got %q (%v)", uri, err)
	}
	f, err := os.Open(parsed.Path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width != 16 {
		t.Fatalf("cached cover unreadable: %v %v", cfg, err)
	}
	m.queue.Replace(tracks, 1)
	if got := m.updateMediaArtwork(); got != "" {
		t.Fatalf("old cover retained: %q", got)
	}
	if _, err := os.Stat(parsed.Path); !os.IsNotExist(err) {
		t.Fatalf("old file not removed: %v", err)
	}
}

func TestMediaArtworkDoesNotLoadWhileClearing(t *testing.T) {
	m := newModel()
	defer m.close()
	defer designArtClose(m)
	m.store, m.demo = storage.New(t.TempDir()), true
	m.queue.Replace([]music.Track{{ID: "fixture", Source: "huawei", CoverURL: "https://covers.dbankcdn.cn/fixture.png"}}, 0)
	designArtCommand(m, designCommand{Op: "art-clear", Index: 1})
	if got := m.updateMediaArtwork(); got != "" || m.mediaArt.done != nil {
		t.Fatalf("artwork load allowed before clear finished: %q", got)
	}
	designArtState(m)
	// Clear must also re-arm a load for the same track after deletion.
	m.updateMediaArtwork()
	designArtCommand(m, designCommand{Op: "art-clear", Index: 1})
	designArtState(m)
	deadline := time.Now().Add(time.Second)
	for len(m.mediaArt.done) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	m.updateMediaArtwork()
	if m.mediaArt.done == nil {
		t.Fatal("same track did not start a fresh load after cache clear")
	}
}

func TestMediaArtworkClearsWhenPausedQueueIsEmptied(t *testing.T) {
	if os.Getenv("HWMUSIC_TEST_MPRIS") == "" {
		t.Skip("requires an isolated session bus")
	}
	m := newModel()
	defer m.close()
	m.mediaController = media.New(func(media.Event) {})
	if m.mediaController == nil {
		t.Fatal("could not register MPRIS")
	}
	m.queue.Replace([]music.Track{{ID: "fixture", Title: "Fixture", Source: "huawei"}}, 0)
	m.updateMedia()
	m.mediaArt.publish(designArtPNG(image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	m.updateMedia()
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	obj := conn.Object("org.mpris.MediaPlayer2.huawei_music_viva", "/org/mpris/MediaPlayer2")
	art := func() string {
		v, err := obj.GetProperty("org.mpris.MediaPlayer2.Player.Metadata")
		if err != nil {
			t.Fatal(err)
		}
		value, _ := v.Value().(map[string]dbus.Variant)["mpris:artUrl"].Value().(string)
		return value
	}
	if art() == "" {
		t.Fatal("missing fixture cover")
	}
	m.clearQueue()
	m.wake()
	if art() != "" {
		t.Fatal("old cover remained after clear")
	}
}
