package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"tinglan/native/internal/audio"
	"tinglan/native/internal/music"
	"tinglan/native/internal/resource"
	"tinglan/native/internal/storage"
)

func (m *model) initAudioCache() {
	if m.store == nil || m.demo || runtime.GOOS != "linux" {
		return
	}
	m.audioCache = storage.NewAudioCache(filepath.Join(m.store.Dir(), "audio-cache"), int64(m.settings.AudioCacheMiB)<<20)
	m.audioCache.SetLimit(int64(m.settings.AudioCacheMiB) << 20)
}
func (m *model) audioCacheScope() string {
	if m.session == nil {
		return ""
	}
	return string(m.session.Source) + ":" + m.session.Account()
}
func audioCacheKey(scope string, l music.SourceLink, tier string) string {
	if scope == "" || l.Source == "" || l.TrackID == "" {
		return ""
	}
	return scope + "\x00" + string(l.Source) + "\x00" + l.TrackID + "\x00" + tier
}

func verifiedCachedPath(cache *storage.AudioCache, key, expected string) (string, string) {
	path, err := cache.Get(key)
	if err != nil {
		return "", ""
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	quality := readAudioQuality(f)
	_ = f.Close()
	if quality == "" || quality != expected {
		cache.Remove(key)
		return "", ""
	}
	return path, quality
}

func readAudioQuality(r io.Reader) string {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return ""
	}
	if string(magic[:]) == "fLaC" {
		return "lossless"
	}
	if string(magic[:3]) == "ID3" || magic[0] == 255 && magic[1]&224 == 224 {
		return "standard"
	}
	return ""
}

func cachedResume(parent context.Context, cache *storage.AudioCache, key, expected string, seconds float64) (*audio.Player, string) {
	path, quality := verifiedCachedPath(cache, key, expected)
	if path == "" {
		return nil, ""
	}
	p, err := audio.OpenLocalFixture(path)
	if err != nil {
		cache.Remove(key)
		return nil, ""
	}
	if seconds <= 0 {
		return p, quality
	}
	state := p.Snapshot()
	if state.Duration <= 0 || seconds >= state.Duration || p.Seek(seconds) != nil {
		p.Close()
		return nil, ""
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	ticks := time.NewTicker(10 * time.Millisecond)
	defer ticks.Stop()
	for {
		state = p.Snapshot()
		if state.Failed {
			p.Close()
			cache.Remove(key)
			return nil, ""
		}
		if state.Ready && math.Abs(state.Position-seconds) < .04 {
			return p, quality
		}
		select {
		case <-ctx.Done():
			p.Close()
			return nil, ""
		case <-ticks.C:
		}
	}
}

func (m *model) cancelAudioCache() {
	if m.audioCacheCancel != nil {
		m.audioCacheCancel()
	}
}
func (m *model) stopAudioCache() {
	m.cancelAudioCache()
	if m.audioCacheDone != nil {
		<-m.audioCacheDone
	}
}
func downloadAudioCache(ctx context.Context, cache *storage.AudioCache, key, url, expected string) error {
	if path, _ := verifiedCachedPath(cache, key, expected); path != "" {
		return nil
	}
	r, err := resource.OpenReader(ctx, url)
	if err != nil {
		return err
	}
	defer r.Close()
	stop := context.AfterFunc(ctx, func() { _ = r.Close() })
	defer stop()
	reader := bufio.NewReader(r)
	magic, err := reader.Peek(4)
	if err != nil || readAudioQuality(bytes.NewReader(magic)) != expected {
		return errors.New("audio cache codec mismatch")
	}
	return cache.Put(ctx, key, r.Size(), reader)
}

func (m *model) fillAudioCache(key, url, expected string) {
	cache := m.audioCache
	if cache == nil || key == "" || m.settings.AudioCacheMiB == 0 {
		return
	}
	if done := m.audioCacheDone; done != nil {
		select {
		case <-done:
		default:
			return
		}
	}
	if path, _ := verifiedCachedPath(cache, key, expected); path != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	done := make(chan struct{})
	m.audioCacheDone, m.audioCacheCancel = done, cancel
	go func() {
		defer close(done)
		defer cancel()
		_ = downloadAudioCache(ctx, cache, key, url, expected)
	}()
}
