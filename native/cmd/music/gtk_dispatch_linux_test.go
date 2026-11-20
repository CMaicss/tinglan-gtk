//go:build linux

package main

import (
	"sync"
	"testing"
)

func TestGTKDispatchCoalescesConcurrentProducers(t *testing.T) {
	var d gtkDispatch
	posts := 0
	post := func() uint32 { posts++; return uint32(posts) }
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() { d.request(post) })
	}
	wg.Wait()
	if posts != 1 || !d.begin() {
		t.Fatalf("posts=%d, missing first drain", posts)
	}
	d.request(post) // work arriving during the drain must not be lost
	if posts != 2 || !d.begin() || d.begin() {
		t.Fatalf("posts=%d, expected exactly one continuation", posts)
	}
}

func TestGTKDispatchClosesWithPendingSource(t *testing.T) {
	var d gtkDispatch
	d.request(func() uint32 { return 7 })
	if got := d.stop(); got != 7 {
		t.Fatalf("pending source = %d", got)
	}
	d.request(func() uint32 { t.Fatal("posted after close"); return 0 })
	if d.begin() || d.stop() != 0 {
		t.Fatal("closed dispatcher admitted work")
	}
}
