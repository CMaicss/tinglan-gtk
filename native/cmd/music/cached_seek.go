package main

import (
	"context"
	"time"

	"tinglan/native/internal/audio"
	"tinglan/native/internal/storage"
)

type cacheSeekState struct {
	cancel  context.CancelFunc
	request uint64
	target  float64
}

func (m *model) cancelCachedSeek() {
	if m.cacheSeek.cancel != nil {
		m.cacheSeek.cancel()
		m.cacheSeek.cancel = nil
	}
	m.cacheSeek.request++
}

func (m *model) startCachedSeek(seconds float64) {
	m.cancelCachedSeek()
	cache, key, original := m.audioCache, m.activeAudioCacheKey, m.player
	if cache == nil || key == "" || original == nil || m.settings.AudioCacheMiB == 0 {
		m.qualityNotice("当前在线音频没有定位表，且没有可用的本地缓存")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	request, generation := m.cacheSeek.request, m.playbackGeneration
	m.cacheSeek.cancel, m.cacheSeek.target = cancel, seconds
	done := m.audioCacheDone
	if _, err := cache.Get(key); err != nil && done != nil {
		select {
		case <-done:
		default:
			m.qualityNotice("等待当前歌曲缓存完成后跳转")
		}
	}
	go func() {
		defer cancel()
		m.prepareCachedSeek(ctx, cache, key, original, done, seconds, request, generation)
	}()
}

func (m *model) prepareCachedSeek(ctx context.Context, cache *storage.AudioCache, key string, original *audio.Player, done <-chan struct{}, seconds float64, request, generation uint64) {
	r := playbackResult{generation: generation, cachedSeek: true, switchRequest: request, seekOriginal: original}
	if _, err := cache.Get(key); err != nil && done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			m.postPlayback(r)
			return
		}
	}
	if ctx.Err() == nil {
		r.player, _ = cachedResume(ctx, cache, key, "lossless", seconds)
	}
	m.postPlayback(r)
}

func (m *model) applyCachedSeek(r playbackResult) {
	if r.switchRequest != m.cacheSeek.request || m.player != r.seekOriginal {
		if r.player != nil {
			go r.player.Close()
		}
		return
	}
	m.cacheSeek.cancel = nil
	if r.player == nil {
		m.qualityNotice("本地缓存还不可用，未跳转；请稍后再试")
		return
	}
	state := r.player.Snapshot()
	if !state.Ready || state.Failed || state.Ended {
		go r.player.Close()
		m.qualityNotice("本地缓存定位失败，继续原播放")
		return
	}
	old := m.player
	old.Pause()
	_ = r.player.SetVolume(m.settings.Volume)
	m.player = r.player
	m.audioState = state
	m.queue.Position = state.Position
	m.lastPoll, m.lastPosition = time.Now(), state.Position
	if m.audioRunning {
		m.player.Play()
	}
	m.pollAudio()
	go old.Close()
}
