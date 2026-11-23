package main

import (
	"context"
	"errors"
	"math"
	"time"

	"tinglan/native/internal/audio"
	"tinglan/native/internal/music"
	"tinglan/native/internal/playback"
)

type designQualitySwitch struct {
	track, preferred, notice string
	serial, request          uint64
	cancel                   context.CancelFunc
	afterLoad                bool
	workers                  chan struct{} // bounds concurrent stream preparation
}

func (m *model) qualityTrackKey() string {
	if track := m.queue.Current(); track != nil {
		return string(track.Source) + ":" + track.ID
	}
	return ""
}

func (m *model) requestedQuality() string {
	if s := &m.qualitySwitch; s.track == m.qualityTrackKey() && s.preferred != "" {
		return s.preferred
	}
	return m.settings.Quality
}

func (m *model) qualityNotice(text string) {
	m.qualitySwitch.notice = text
	m.qualitySwitch.serial++
}

func (m *model) qualitySwitchFailed(tier string) {
	label := "标准"
	if tier == "lossless" {
		label = "无损"
	}
	m.qualityNotice("当前歌曲无法切换为" + label)
}

func (m *model) cancelQualitySwitch() {
	s := &m.qualitySwitch
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.afterLoad = false
	s.request++
}

func (m *model) switchDesignQuality(id, quality string) {
	if id == "" || id != m.qualityTrackKey() || (quality != "standard" && quality != "lossless") {
		return
	}
	s := &m.qualitySwitch
	m.cancelQualitySwitch()
	s.track, s.preferred = id, quality
	if m.player == nil {
		// Let the initial request finish. A rejected upgrade must not turn
		// a playable song into a failed queue entry.
		s.afterLoad = m.resolving
		return
	}
	if m.actualQuality == quality {
		return
	}
	row, ok := m.canonicalFor(*m.queue.Current())
	resolver := m.resolver
	if !ok || resolver == nil {
		m.qualitySwitchFailed(quality)
		return
	}
	// Switch the encoding of the playing source, not the recording/provider.
	if m.playedLink.TrackID != "" {
		row.Links = []music.SourceLink{m.playedLink}
	}
	if s.workers == nil {
		s.workers = make(chan struct{}, 2)
	}
	workers := s.workers
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	cache, scope, cacheDone := m.audioCache, m.audioCacheScope(), m.audioCacheDone
	cacheEnabled := cache != nil && m.settings.AudioCacheMiB > 0
	if cacheEnabled {
		m.cancelAudioCache()
	}
	s.cancel = cancel
	old := m.player
	request, generation := s.request, m.playbackGeneration
	go func() {
		result := playbackResult{generation: generation, qualitySwitch: true, switchRequest: request}
		defer func() { m.postPlayback(result); cancel() }()
		select {
		case workers <- struct{}{}:
			defer func() { <-workers }()
		case <-ctx.Done():
			result.err = ctx.Err()
			return
		}
		var resolved playback.Result
		resolved, result.err = resolver.Resolve(ctx, row, quality)
		if result.err != nil {
			return
		}
		result.playback, result.link = resolved.Playback, resolved.Link
		if resolved.Access == music.AccessPreview {
			result.err = errUnavailableQuality
			return
		}
		result.player, result.playback.Quality, result.err = openRemote(ctx, result.playback.URL)
		if result.err != nil {
			return
		}
		result.err = prepareQualityPosition(ctx, old, result.player, quality, result.playback.Quality)
		if cacheEnabled {
			result.audioCacheKey = audioCacheKey(scope, result.link, result.playback.Quality)
		}
		if !errors.Is(result.err, audio.ErrOnlineSeekNeedsCache) || result.audioCacheKey == "" {
			return
		}
		result.player.Close()
		result.player = nil
		if cacheDone != nil {
			select {
			case <-cacheDone:
			case <-ctx.Done():
				result.err = ctx.Err()
				return
			}
		}
		if result.err = downloadAudioCache(ctx, cache, result.audioCacheKey, result.playback.URL, quality); result.err != nil {
			return
		}
		current := old.Snapshot()
		if !current.Ready || current.Failed || current.Ended {
			result.err = errUnavailableQuality
			return
		}
		result.player, result.playback.Quality = cachedResume(ctx, cache, result.audioCacheKey, quality, current.Position)
		if result.player == nil {
			result.err = errUnavailableQuality
			return
		}
		result.fromAudioCache = true
	}()
}

var errUnavailableQuality = errors.New("requested quality unavailable")

// The candidate stays paused while its decoder seeks. Do not stop the old
// stream until the candidate confirms the seek, including on asynchronous decoders.
func prepareQualityPosition(ctx context.Context, old, candidate *audio.Player, requested, actual string) error {
	if requested != actual {
		return errUnavailableQuality
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	wait := func(ok func(audio.State) bool) error {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			state := candidate.Snapshot()
			if state.Failed {
				return errUnavailableQuality
			}
			if ok(state) {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
	if err := wait(func(s audio.State) bool { return s.Ready && s.Duration > 0 }); err != nil {
		return err
	}
	before, replacement := old.Snapshot(), candidate.Snapshot()
	if before.Ended || (before.Duration > 0 && replacement.Duration < before.Duration-5) || before.Position >= replacement.Duration {
		return errUnavailableQuality
	}
	if before.Position <= .02 {
		return nil
	}
	if err := candidate.Seek(before.Position); err != nil {
		return err
	}
	return wait(func(s audio.State) bool { return math.Abs(s.Position-before.Position) < .05 })
}

func (m *model) applyQualityResult(r playbackResult) {
	s := &m.qualitySwitch
	if r.switchRequest != s.request || m.player == nil {
		if r.player != nil {
			r.player.Close()
		}
		return
	}
	s.cancel = nil
	if r.err != nil || r.player == nil {
		if r.player != nil {
			r.player.Close()
		}
		m.qualitySwitchFailed(s.preferred)
		return
	}
	old := m.player
	candidate := r.player.Snapshot()
	if !candidate.Ready || candidate.Failed || candidate.Ended {
		r.player.Close()
		m.qualitySwitchFailed(s.preferred)
		return
	}
	current := old.Snapshot()
	if current.Ended || math.Abs(current.Position-candidate.Position) > 2 {
		r.player.Close()
		m.qualitySwitchFailed(s.preferred)
		return
	}
	m.cancelCachedSeek()
	m.activeAudioCacheKey = r.audioCacheKey
	old.Pause()
	_ = r.player.SetVolume(m.settings.Volume)
	m.player, m.actualQuality, m.playedLink = r.player, r.playback.Quality, r.link
	m.currentAccess = r.playback.Access
	m.resolvedDuration = candidate.Duration
	m.lastPoll, m.lastPosition = time.Now(), candidate.Position
	if m.audioRunning {
		m.player.Play()
	}
	m.pollAudio()
	if !r.fromAudioCache && r.audioCacheKey != "" {
		m.fillAudioCache(r.audioCacheKey, r.playback.URL, r.playback.Quality)
	}
	go old.Close()
}
