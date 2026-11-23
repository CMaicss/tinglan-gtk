package main

import (
	"context"
	"sync"
	"time"

	"tinglan/native/internal/lyrics"
	"tinglan/native/internal/music"
	"tinglan/native/internal/storage"
)

type designLyricLoad struct {
	token, phase string
	published    bool
	requested    bool
	cancel       context.CancelFunc
	done         <-chan designLyricResult
	workers      sync.WaitGroup
}

type designLyricResult struct {
	storage.LyricDocument
	offset, originalOffset float64
}

// Snapshot-driven: the first UI request schedules disk I/O, never waits for it.
// A cache miss is not permission to contact a provider during startup.
func (m *model) updateDesignLyrics(request string) string {
	s := &m.designLyrics
	track := m.queue.Current()
	token := designLyricToken(m)
	if m.player != nil {
		token = ""
	}
	if token != s.token {
		if s.cancel != nil {
			s.cancel()
		}
		if s.published && m.player == nil && !m.resolving {
			m.lyricLines, m.lyricSource, m.lyricOffset = nil, "", 0
		}
		s.published = false
		s.token, s.phase, s.requested, s.cancel, s.done = token, "", false, nil, nil
	}
	if token == "" || track == nil || m.store == nil || m.demo {
		return ""
	}
	if len(m.lyricLines) != 0 {
		return ""
	}
	if request == token {
		s.requested = true
	}
	if s.done != nil {
		select {
		case doc := <-s.done:
			s.done, s.cancel = nil, nil
			if len(doc.Lines) != 0 {
				m.lyricLines, m.lyricSource = doc.Lines, doc.Source
				if m.lyricOffset == doc.originalOffset {
					m.lyricOffset = doc.offset
				}
				s.phase, s.published = "ready", true
				return ""
			}
			if s.phase == "cache" {
				s.phase = "idle"
			} else {
				s.phase = "unavailable"
			}
		default:
		}
	}
	switch s.phase {
	case "":
		key, store := m.trackLyricsKey(*track), m.store
		m.startDesignLyrics("cache", *track, func(_ context.Context) storage.LyricDocument {
			doc, _ := store.LoadLyrics(key)
			if len(doc.Lines) == 0 {
				doc.Lines, _ = lyrics.ParseAny(doc.Text)
			}
			return doc
		})
	case "idle":
		if !s.requested {
			break
		}
		row, _ := m.canonicalFor(*track)
		links := append([]music.SourceLink(nil), row.Links...)
		query := lyrics.Query{Title: track.Title, Artist: track.ArtistText()}
		if track.Duration != nil {
			query.Duration = *track.Duration
		}
		fallback := m.settings.ThirdPartyLyrics
		m.startDesignLyrics("online", *track, func(ctx context.Context) storage.LyricDocument {
			for _, link := range links {
				if ctx.Err() != nil {
					break
				}
				if link.Disabled {
					continue
				}
				if lines, label := m.sourceLyrics(ctx, link); len(lines) > 0 {
					return storage.LyricDocument{Lines: lines, Source: label}
				}
			}
			if fallback && ctx.Err() == nil {
				text, label := lyrics.Fallback(ctx, query)
				lines, _ := lyrics.ParseAny(text)
				return storage.LyricDocument{Lines: lines, Text: text, Source: label}
			}
			return storage.LyricDocument{}
		})
	}
	return s.phase
}

func (m *model) startDesignLyrics(phase string, track music.Track, load func(context.Context) storage.LyricDocument) {
	s := &m.designLyrics
	store, offsetKey, original := m.store, m.trackOffsetKey(track), m.lyricOffset
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	done := make(chan designLyricResult, 1)
	s.phase, s.cancel, s.done = phase, cancel, done
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer cancel()
		doc := load(ctx)
		result := designLyricResult{LyricDocument: doc, originalOffset: original}
		if len(doc.Lines) != 0 && ctx.Err() == nil {
			result.offset = loadLyricOffset(store, offsetKey)
		}
		done <- result
	}()
}

func (s *designLyricLoad) invalidate() {
	if s.cancel != nil {
		s.cancel()
	}
	s.phase, s.requested, s.cancel, s.done = "", false, nil, nil
}

func (s *designLyricLoad) close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.workers.Wait()
}
