package main

import (
	"context"
	"errors"
	"testing"
)

func TestSearchTracksWithoutCatalog(t *testing.T) {
	tracks, next, err := searchTracks(context.Background(), nil, "test", 20)
	if len(tracks) != 0 || next != 20 || !errors.Is(err, errNoSource) {
		t.Fatalf("no catalog: tracks=%d, next=%d, err=%v", len(tracks), next, err)
	}
}
