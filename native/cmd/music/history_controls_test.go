package main

import (
	"testing"
	"tinglan/native/internal/music"
)

func TestHistoryPlaybackStartsHistoryQueue(t *testing.T) {
	m := newModel()
	m.enableDemo(2)
	m.queue.Replace([]music.Track{{ID: "old", Title: "Old"}}, 0)
	m.localHistory = syntheticTracks(2)
	m.playHistoryTrack(-1)
	if m.queue.Current().ID != "old" {
		t.Fatal("invalid history row changed the queue")
	}
	m.playHistoryTrack(1)
	if len(m.queue.Tracks) != 2 || m.queue.Index != 1 || m.queue.Tracks[0].ID != m.localHistory[0].ID || m.queue.Current().ID != m.localHistory[1].ID {
		t.Fatalf("history queue mismatch: %+v", m.queue)
	}
}
