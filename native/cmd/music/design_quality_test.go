package main

import (
	"testing"

	"tinglan/native/internal/audio"
	"tinglan/native/internal/music"
)

func TestDesignQualityFailedSwitchNamesTargetAndKeepsCurrent(t *testing.T) {
	for tier, want := range map[string]string{"lossless": "当前歌曲无法切换为无损", "standard": "当前歌曲无法切换为标准"} {
		m := newModel()
		original := new(audio.Player)
		m.player = original
		m.qualitySwitch.preferred = tier
		m.applyQualityResult(playbackResult{err: errUnavailableQuality})
		if m.player != original || m.qualitySwitch.notice != want || m.qualitySwitch.serial != 1 {
			t.Errorf("unexpected failure feedback for %s: %q", tier, m.qualitySwitch.notice)
		}
		m.player = nil // the sentinel is not an opened player
		m.close()
	}
}

func TestDesignQualityRequestDuringLoadDoesNotRestartResolve(t *testing.T) {
	m := newModel()
	defer m.close()
	m.queue.Replace([]music.Track{{ID: "one", Title: "One", Source: "huawei"}}, 0)
	m.resolving = true
	generation := m.playbackGeneration
	runDesignCommand(m, designCommand{Op: "quality-switch", ID: "huawei:one", Text: "lossless"})
	if !m.qualitySwitch.afterLoad || m.playbackGeneration != generation || !m.resolving {
		t.Fatal("quality selection interfered with the initial resolve")
	}
}

func TestDesignQualitySelectionIsForCurrentSongOnly(t *testing.T) {
	m := newModel()
	defer m.close()
	m.settings.Quality = "standard"
	m.queue.Replace([]music.Track{{ID: "one", Title: "One", Source: "huawei"}}, 0)
	runDesignCommand(m, designCommand{Op: "quality-switch", ID: "huawei:other", Text: "lossless"})
	runDesignCommand(m, designCommand{Op: "quality-switch", ID: "huawei:one", Text: "bogus"})
	if got := designSnapshot(m).QualityRequested; got != "standard" {
		t.Fatalf("invalid choice changed quality to %q", got)
	}
	runDesignCommand(m, designCommand{Op: "quality-switch", ID: "huawei:one", Text: "lossless"})
	if got := designSnapshot(m).QualityRequested; got != "lossless" || m.settings.Quality != "standard" || m.player != nil || m.resolving {
		t.Fatalf("selection changed default or started playback: %q", got)
	}
	m.queue.Replace([]music.Track{{ID: "two", Title: "Two", Source: "huawei"}}, 0)
	if got := designSnapshot(m).QualityRequested; got != "standard" {
		t.Fatalf("override leaked to another song: %q", got)
	}
}
