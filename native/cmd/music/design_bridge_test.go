package main

import (
	"testing"
	"tinglan/native/internal/music"
)

func TestDesignBridgeCanRemoveTrackFromDisabledSource(t *testing.T) {
	m := newModel()
	defer m.close()
	tracks := []music.Track{{ID: "selected"}, {ID: "unavailable", Source: "disabled-fixture"}}
	m.queue.Replace(tracks, 0)
	cmd := designCommand{Op: "queue-remove", Index: 1, ID: "disabled-fixture:stale"}
	runDesignCommand(m, cmd)
	if len(m.queue.Tracks) != 2 {
		t.Fatal("wrong identity removed")
	}
	cmd.ID = "disabled-fixture:unavailable"
	runDesignCommand(m, cmd)
	if len(m.queue.Tracks) != 1 || m.queue.Tracks[0].ID != "selected" {
		t.Fatal("disabled source prevented matching row removal")
	}
}

func TestDesignBridgeSearchUsesModel(t *testing.T) {
	m := newModel()
	m.enableDemo(2)
	defer m.close()
	runDesignCommand(m, designCommand{Op: "search", Text: "FLAC"})
	state := designSnapshot(m)
	if len(state.Favorites) != 2 || len(state.Results) != 1 || state.Results[0].Title != "Smoke FLAC" {
		t.Fatalf("real model did not supply both collections: %+v", state)
	}
	runDesignCommand(m, designCommand{Op: "search", Text: ""})
	if len(designSnapshot(m).Results) != 0 {
		t.Fatal("clearing search left stale results")
	}
}
