//go:build linux

package main

import "sync"

// gtkDispatch owns one asynchronous GLib idle source. The mutex only protects
// source publication, never model or widget work. post must not invoke inline.
type gtkDispatch struct {
	mu      sync.Mutex
	pending uint32
	stopped bool
}

func (d *gtkDispatch) request(post func() uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.stopped && d.pending == 0 {
		d.pending = post()
	}
}

func (d *gtkDispatch) begin() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || d.pending == 0 {
		return false
	}
	d.pending = 0
	return true
}

func (d *gtkDispatch) stop() uint32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	id := d.pending
	d.pending = 0
	return id
}
