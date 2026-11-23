//go:build linux && cgo

package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tinglan/native/internal/audio"
	"tinglan/native/internal/music"
)

func TestDesignQualityHandoffKeepsPositionAndPause(t *testing.T) {
	dir := os.Getenv("HWMUSIC_AUDIO_FIXTURES")
	if dir == "" {
		t.Skip("synthetic audio fixtures required")
	}
	open := func(name string) *audio.Player {
		t.Helper()
		p, err := audio.OpenLocalFixture(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	m := newModel()
	defer m.close()
	m.queue.Replace([]music.Track{{ID: "one", Title: "One", Source: "fixture"}}, 0)
	m.player, m.actualQuality = open("tone.mp3"), "standard"
	old := m.player
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := old.Seek(1.25); err != nil {
		t.Fatal(err)
	}
	for math.Abs(old.Snapshot().Position-1.25) > .05 {
		if ctx.Err() != nil {
			t.Fatal("old player seek incomplete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	next := open("tone.flac")
	err := prepareQualityPosition(ctx, old, next, "lossless", "standard")
	if err == nil {
		t.Fatal("accepted an MP3 as lossless")
	}
	m.applyQualityResult(playbackResult{player: next, err: err})
	if m.player != old {
		t.Fatal("failed switch replaced original player")
	}
	next = open("tone.flac")
	if err := prepareQualityPosition(ctx, old, next, "lossless", "lossless"); err != nil {
		next.Close()
		t.Fatal(err)
	}
	m.applyQualityResult(playbackResult{player: next, playback: music.Playback{Quality: "lossless"}})
	if m.player != next || m.audioState.Playing || math.Abs(m.audioState.Position-1.25) > .05 || m.actualQuality != "lossless" {
		t.Fatalf("unexpected handoff: %+v, %s", m.audioState, m.actualQuality)
	}
	m.audioRunning = true
	m.player.Play()
	back := open("tone.mp3")
	if err := prepareQualityPosition(ctx, next, back, "standard", "standard"); err != nil {
		back.Close()
		t.Fatal(err)
	}
	m.applyQualityResult(playbackResult{player: back, playback: music.Playback{Quality: "standard"}})
	if m.player != back || !m.audioState.Playing || m.audioState.Position < 1.2 {
		t.Fatalf("switch did not keep playing from current position: %+v", m.audioState)
	}
}
