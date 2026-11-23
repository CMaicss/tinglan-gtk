package main

import (
	"testing"
	"time"

	"tinglan/native/internal/lyrics"
	"tinglan/native/internal/music"
	"tinglan/native/internal/storage"
)

func TestDesignLyricsCacheMissWaitsForCurrentPanelToken(t *testing.T) {
	m := newModel()
	defer m.close()
	m.store = storage.New(t.TempDir())
	m.settings.ThirdPartyLyrics = false
	m.queue.Replace([]music.Track{{ID: "fixture", Title: "Fixture", Source: "huawei"}}, 0)
	first := designSnapshot(m)
	if first.LyricPhase != "cache" {
		t.Fatalf("initial phase = %q", first.LyricPhase)
	}
	m.designLyrics.workers.Wait()
	for _, token := range []string{"", "stale-token", ""} {
		if token != "" {
			runDesignCommand(m, designCommand{Op: "lyrics-open", ID: token})
		}
		if got := designSnapshot(m).LyricPhase; got != "idle" {
			t.Fatalf("phase for %q = %q, want idle", token, got)
		}
	}
	runDesignCommand(m, designCommand{Op: "lyrics-open", ID: designLyricToken(m)})
	m.designLyrics.workers.Wait()
	if got := designSnapshot(m); got.LyricPhase != "unavailable" || got.Playing || m.player != nil {
		t.Fatalf("expected completed offline lookup without starting playback, phase: %q", got.LyricPhase)
	}
}

func TestDesignClearLocalLyricsDiscardsCompletedCacheRead(t *testing.T) {
	m := newModel()
	defer m.close()
	m.store = storage.New(t.TempDir())
	track := music.Track{ID: "fixture", Title: "Fixture", Source: "huawei"}
	m.queue.Replace([]music.Track{track}, 0)
	result := make(chan designLyricResult, 1)
	result <- designLyricResult{LyricDocument: storage.LyricDocument{Lines: lyrics.Parse("[00:00.00]stale")}}
	m.designLyrics.token, m.designLyrics.phase, m.designLyrics.done = designLyricToken(m), "cache", result
	runDesignCommand(m, designCommand{Op: "settings-lyrics-clear-local", Index: 1})
	if got := designSnapshot(m); len(got.Lyrics) != 0 {
		t.Fatalf("cleared result was republished: %+v", got.Lyrics)
	}
}

func TestDesignRestoresCachedLyricsWithoutPlaying(t *testing.T) {
	dir := t.TempDir()
	s := storage.New(dir)
	track := music.Track{ID: "fixture", Title: "Fixture", Source: "huawei"}
	if err := s.SaveQueue(storage.SavedQueue{Tracks: []music.Track{track}, Index: 0}); err != nil {
		t.Fatal(err)
	}
	m := newModel()
	defer m.close()
	m.store = s
	if err := s.SaveLyrics(m.trackLyricsKey(track), storage.LyricDocument{Text: "[00:00.00]cached lyric"}, true); err != nil {
		t.Fatal(err)
	}
	m.openProfile(dir)
	deadline := time.Now().Add(time.Second)
	for {
		m.wake()
		state := designSnapshot(m)
		if state.Playing || m.player != nil || m.resolving {
			t.Fatal("restoring lyrics started playback")
		}
		if state.Current == 0 && len(state.Lyrics) == 1 && state.Lyrics[0].Text == "cached lyric" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("restored current=%d without cached lyrics: %+v", state.Current, state.Lyrics)
		}
		time.Sleep(time.Millisecond)
	}
}
