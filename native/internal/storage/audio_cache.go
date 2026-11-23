package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const MaxAudioCacheMiB = 16384

type AudioCache struct {
	dir             string
	mu              sync.Mutex
	limit, reserved int64
	cancel          context.CancelFunc
	used            atomic.Int64
}

func NewAudioCache(dir string, limit int64) *AudioCache {
	c := &AudioCache{dir: dir, limit: max(0, limit)}
	// The desktop owns this cache under the profile's single-instance lock.
	// A crash can leave a private staging file; never treat it as playable.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".pending-") && entry.Type().IsRegular() {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
	}
	_ = c.evict(c.limit)
	return c
}

// UsageBytes reports complete cache files, without reading the directory on each UI poll.
func (c *AudioCache) UsageBytes() int64 { return c.used.Load() }
func (c *AudioCache) path(key string) string {
	return filepath.Join(c.dir, fmt.Sprintf("%x.audio", sha256.Sum256([]byte(key))))
}

func (c *AudioCache) Get(key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limit <= 0 || key == "" {
		return "", os.ErrNotExist
	}
	p := c.path(key)
	s, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !s.Mode().IsRegular() || s.Size() == 0 || s.Size() > c.limit {
		return "", os.ErrNotExist
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now)
	return p, nil
}

func (c *AudioCache) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.path(key)
	if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() && os.Remove(p) == nil {
		c.used.Add(-info.Size())
	}
}
func (c *AudioCache) SetLimit(limit int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limit = max(0, limit)
	if c.reserved > c.limit && c.cancel != nil {
		c.cancel()
	}
	_ = c.evict(max(0, c.limit-c.reserved))
}

func (c *AudioCache) evict(limit int64) error {
	entries, err := os.ReadDir(c.dir)
	if os.IsNotExist(err) {
		c.used.Store(0)
		return nil
	}
	if err != nil {
		return err
	}
	var total int64
	defer func() { c.used.Store(total) }()
	type record struct {
		name string
		size int64
		used time.Time
	}
	var files []record
	for _, e := range entries {
		name := e.Name()
		if len(name) != 70 || filepath.Ext(name) != ".audio" {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		total += info.Size()
		files = append(files, record{name, info.Size(), info.ModTime()})
	}
	slices.SortFunc(files, func(a, b record) int {
		if n := a.used.Compare(b.used); n != 0 {
			return n
		}
		if a.name < b.name {
			return -1
		}
		if a.name > b.name {
			return 1
		}
		return 0
	})
	for _, f := range files {
		if total <= limit {
			break
		}
		if err := os.Remove(filepath.Join(c.dir, f.name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= f.size
	}
	return nil
}

// Put publishes only after all expected bytes have arrived. One in-flight
// entry reserves space inside the same budget as published entries.
func (c *AudioCache) Put(parent context.Context, key string, size int64, r io.Reader) error {
	c.mu.Lock()
	if key == "" || size <= 0 || size > c.limit || c.reserved != 0 {
		c.mu.Unlock()
		return os.ErrInvalid
	}
	if err := os.MkdirAll(c.dir, 0700); err != nil {
		c.mu.Unlock()
		return err
	}
	if err := c.evict(c.limit - size); err != nil {
		c.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	c.cancel, c.reserved = cancel, size
	c.mu.Unlock()
	defer func() { cancel(); c.mu.Lock(); c.reserved, c.cancel = 0, nil; c.mu.Unlock() }()
	tmp, err := os.CreateTemp(c.dir, ".pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	buf := make([]byte, 128*1024)
	remaining := size
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.ReadFull(r, buf[:min(int64(len(buf)), remaining)])
		if err != nil {
			if err == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if _, err := tmp.Write(buf[:n]); err != nil {
			return err
		}
		remaining -= int64(n)
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if size > c.limit {
		return os.ErrInvalid
	}
	path := c.path(key)
	var oldSize int64
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		oldSize = info.Size()
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	c.used.Add(size - oldSize)
	return nil
}
