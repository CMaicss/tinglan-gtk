package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestAudioCacheCompleteEntriesEvictByUse(t *testing.T) {
	c := NewAudioCache(t.TempDir(), 8)
	save := func(id string) {
		t.Helper()
		if err := c.Put(context.Background(), id, 4, bytes.NewReader([]byte(id+"abc"))); err != nil {
			t.Fatal(err)
		}
	}
	save("a")
	save("b")
	if err := os.Chtimes(c.path("b"), time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if p, err := c.Get("a"); err != nil || p == "" {
		t.Fatal(err)
	}
	save("c")
	if got := c.UsageBytes(); got != 8 {
		t.Fatalf("usage after eviction = %d, want 8", got)
	}
	if got := NewAudioCache(c.dir, 8).UsageBytes(); got != 8 {
		t.Fatalf("usage after reopening = %d, want 8", got)
	}
	if _, err := c.Get("b"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old entry retained: %v", err)
	}
	if _, err := c.Get("a"); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(context.Background(), "short", 4, bytes.NewReader([]byte("x"))); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short: %v", err)
	}
	if _, err := c.Get("short"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial exposed: %v", err)
	}
	c.SetLimit(0)
	if _, err := c.Get("a"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled cache hit: %v", err)
	}
}
