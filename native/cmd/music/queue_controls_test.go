package main

import (
	"testing"
	"tinglan/native/internal/music"
)

func TestQueueControlsPreserveSelectionAndOrder(t *testing.T) {
	m := newModel()
	m.enableDemo(2)
	m.queue.Replace([]music.Track{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}, {ID: "c", Title: "C"}}, 0)
	m.selectQueueTrack(1)
	if got := m.queue.Current(); got == nil || got.ID != "b" {
		t.Fatalf("current = %v", got)
	}
	m.removeQueueTrack(0)
	if got := m.queue.Current(); got == nil || got.ID != "b" || m.queue.Index != 0 {
		t.Fatalf("current = %v, index = %d", got, m.queue.Index)
	}
	m.removeQueueTrack(0)
	if got := m.queue.Current(); got == nil || got.ID != "c" {
		t.Fatalf("replacement = %v", got)
	}
	m.clearQueue()
	if len(m.queue.Tracks) != 0 || m.queue.Current() != nil || m.queue.Position != 0 {
		t.Fatalf("queue not cleared: %+v", m.queue)
	}
}
