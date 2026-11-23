//go:build linux && cgo

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tinglan/native/internal/audio"

	"tinglan/native/internal/storage"
)

func TestWrongCodecIsNotACompleteCacheHit(t *testing.T) {
	c := storage.NewAudioCache(t.TempDir(), 1024)
	ctx := context.Background()
	const key = "lossless-entry"
	if err := c.Put(ctx, key, 4, strings.NewReader("ID3x")); err != nil {
		t.Fatal(err)
	}
	if err := downloadAudioCache(ctx, c, key, "not-a-resource-url", "lossless"); err == nil {
		t.Fatal("mismatched MP3 suppressed the FLAC fetch")
	}
	if _, err := c.Get(key); !os.IsNotExist(err) {
		t.Fatalf("bad cache entry survived: %v", err)
	}
	if err := c.Put(ctx, key, 4, strings.NewReader("fLaC")); err != nil {
		t.Fatal(err)
	}
	if err := downloadAudioCache(ctx, c, key, "not-a-resource-url", "lossless"); err != nil {
		t.Fatalf("verified FLAC should be reused: %v", err)
	}
}

func TestCachedSeekWaitsForPublishThenSwapsPausedPlayer(t *testing.T) {
	dir := os.Getenv("HWMUSIC_AUDIO_FIXTURES")
	if dir == "" {
		t.Skip("synthetic audio fixtures required")
	}
	path := filepath.Join(dir, "tone.flac")
	original, err := audio.OpenLocalFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel()
	m.player = original
	defer m.close()
	m.audioCache = storage.NewAudioCache(t.TempDir(), 2<<20)
	m.activeAudioCacheKey = "song"
	done := make(chan struct{})
	m.audioCacheDone = done
	m.startCachedSeek(1.25)
	if m.player != original || m.cacheSeek.cancel == nil {
		close(done)
		t.Fatal("seek did not retain the original while awaiting cache")
	}
	f, err := os.Open(path)
	if err != nil {
		close(done)
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		close(done)
		t.Fatal(err)
	}
	err = m.audioCache.Put(context.Background(), "song", st.Size(), f)
	close(done)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(3 * time.Second)
	for m.cacheSeek.cancel != nil && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
		m.applyPlaybackResults()
	}
	if m.player == original || m.cacheSeek.cancel != nil || m.audioState.Playing || m.audioState.Position < 1.21 || m.audioState.Position > 1.29 {
		t.Fatalf("handoff failed: %+v", m.audioState)
	}
}

func TestAudioCacheReopenAndResumeLocalFLAC(t *testing.T) {
	dir := os.Getenv("HWMUSIC_AUDIO_FIXTURES")
	if dir == "" {
		t.Skip("synthetic audio fixtures required")
	}
	f, err := os.Open(filepath.Join(dir, "tone.flac"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	c := storage.NewAudioCache(root, 2<<20)
	if err = c.Put(context.Background(), "song", st.Size(), f); err != nil {
		t.Fatal(err)
	}
	c = storage.NewAudioCache(root, 2<<20)
	p, codec := cachedResume(context.Background(), c, "song", "lossless", 1.25)
	if p == nil {
		t.Fatal("cache hit failed to resume")
	}
	defer p.Close()
	if s := p.Snapshot(); s.Position < 1.21 || s.Position > 1.29 || s.Playing || codec != "lossless" {
		t.Fatalf("position/format wrong: %+v %s", s, codec)
	}
	if missing, _ := cachedResume(context.Background(), c, "missing", "", 1.25); missing != nil {
		missing.Close()
		t.Fatal("unexpected cache hit")
	}
}
