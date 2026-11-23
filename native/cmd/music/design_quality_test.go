package main

import (
	"testing"

	"tinglan/native/internal/music"
)

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
