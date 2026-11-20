package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/vivaui/viva/app/singleinstance"
	"tinglan/native/internal/appmeta"
	"tinglan/native/internal/lyrics"
	"tinglan/native/internal/media"
	"tinglan/native/internal/music"
)

type designCommand struct {
	Op, Text, ID string
	Index        int
	Value        float64
}
type designTrack struct{ ID, Title, Artist, Album, Duration string }
type designState struct {
	Collections, Settings, Art                                 any
	Lyrics                                                     []lyrics.Line
	Favorites, Results, History, Queue, Detail                 []designTrack
	Playlists                                                  []designPlaylist
	Sources                                                    []string
	Current                                                    int
	Position, Duration, Volume                                 float64
	Playing, SignedIn, Restoring, Searching, More, SearchError bool
	Raise, Quit                                                bool
	Status                                                     string
}
type designPlaylist struct{ ID, Name, Subtitle string }

func designTracks(tracks []music.Track) []designTrack {
	out := make([]designTrack, 0, len(tracks))
	for _, t := range tracks {
		album := ""
		if t.Album != nil {
			album = t.Album.Title
		}
		out = append(out, designTrack{string(t.Source) + ":" + t.ID, t.Title, t.ArtistText(), album, durationText(t.Duration)})
	}
	return out
}

func designSnapshot(m *model) designState {
	return designSnapshotWithWindow(m, false)
}

func designSnapshotWithWindow(m *model, quit bool) designState {
	collections, settings, art := designCollectionState(m), designSettingsState(m), designArtState(m)
	s := designState{Collections: collections, Settings: settings, Art: art, Favorites: designTracks(m.tracks), Results: designTracks(m.filtered), History: designTracks(m.localHistory), Queue: designTracks(m.queue.Tracks), Detail: designTracks(m.localTracks), Current: -1, Position: m.audioState.Position, Duration: m.audioState.Duration, Volume: m.settings.Volume, Playing: m.audioState.Playing, SignedIn: m.session != nil, Restoring: m.restoring, Searching: m.searchLoading, More: m.searchMore, SearchError: m.searchError, Status: m.status}
	s.Lyrics = m.lyricLines
	s.Raise, s.Quit = m.reopen.Swap(false), quit
	if m.queue.Current() != nil {
		s.Current = m.queue.Index
	}
	if m.registry != nil {
		for _, src := range m.registry.SourceList() {
			s.Sources = append(s.Sources, string(src.ID()))
		}
	}
	for _, p := range m.localPlaylists {
		kind := "本地"
		if !p.Local() {
			kind = sourceLabel(p.Source) + " · 只读"
		}
		s.Playlists = append(s.Playlists, designPlaylist{p.ID, p.Name, kind})
	}
	return s
}

func runDesignCommand(m *model, c designCommand) {
	match := func(tracks []music.Track) bool {
		if c.Index < 0 || c.Index >= len(tracks) {
			return false
		}
		t := tracks[c.Index]
		return c.ID == string(t.Source)+":"+t.ID && (c.Op == "queue-remove" || m.trackAvailable(t))
	}
	switch c.Op {
	case "search":
		m.query.SetText(c.Text)
		m.search()
		if m.searchQuery == "" {
			m.filtered = nil
		}
	case "more":
		m.moreSearch()
	case "play-favorites":
		if match(m.tracks) {
			m.startTracks(m.tracks, c.Index)
		}
	case "play-detail":
		if match(m.localTracks) {
			m.startTracks(m.localTracks, c.Index)
		}
	case "play-result":
		if match(m.filtered) {
			m.view = 2
			m.playListTrack(m.filtered, c.Index)
		}
	case "play-history":
		if match(m.localHistory) {
			m.playHistoryTrack(c.Index)
		}
	case "playlist":
		for _, p := range m.localPlaylists {
			if p.ID == c.ID {
				m.openPlaylist(p)
				break
			}
		}
	case "toggle":
		m.togglePlayback()
	case "step":
		if c.Index == -1 || c.Index == 1 {
			m.stepPlayback(c.Index)
		}
	case "mode":
		if c.Index >= 0 && c.Index < 4 {
			m.settings.RepeatMode = [...]string{"off", "all", "one", "off"}[c.Index]
			m.settings.Shuffle = c.Index == 3
			m.queue.SetShuffle(m.settings.Shuffle)
			m.saveSettings()
		}
	case "seek":
		m.seekPlayback(c.Value)
	case "volume":
		m.settings.Volume = max(0, min(1, c.Value))
		if m.player != nil && !m.demo {
			_ = m.player.SetVolume(m.settings.Volume)
		}
		m.saveSettings()
	case "queue-select":
		if match(m.queue.Tracks) {
			m.selectQueueTrack(c.Index)
		}
	case "queue-remove":
		if match(m.queue.Tracks) {
			m.removeQueueTrack(c.Index)
		}
	case "queue-clear":
		m.clearQueue()
	default:
		if !designCollectionCommand(m, c) && !designSettingsCommand(m, c) {
			designArtCommand(m, c)
		}
	}
}

func runDesignBridge(m *model, profile string, in io.Reader, out io.Writer) error {
	if !m.demo {
		claim, primary, err := singleinstance.SingleInstance("org.tinglan.unofficial.viva", nil, func([]string) { m.reopen.Store(true); m.requestUpdate() })
		if err != nil {
			return err
		}
		if !primary {
			return errors.New("听澜已在运行，请先退出已有实例")
		}
		defer claim.Close()
		if profile == "" {
			if err := appmeta.MigrateDataDir(); err != nil {
				return err
			}
			profile = defaultProfile()
			if profile == "" {
				return errors.New("找不到用户配置目录")
			}
			if err := os.MkdirAll(profile, 0o700); err != nil {
				return err
			}
		}
		if _, err := os.Stat(profile); err != nil {
			return err
		}
		m.openProfile(profile)
	}
	defer m.close()
	defer designCollectionClose(m)
	defer designSettingsClose(m)
	defer designArtClose(m)
	wake := make(chan struct{}, 1)
	notify := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	m.uiWake.Store(&notify)
	defer m.uiWake.Store(nil)
	quit := false
	m.quit = func() { quit = true }
	if !m.demo {
		m.mediaController = media.New(func(e media.Event) {
			if e.Command == 6 {
				m.reopen.Store(true)
				notify()
				return
			}
			m.mu.Lock()
			if !m.closed {
				m.mediaEvents = append(m.mediaEvents, e)
			}
			m.mu.Unlock()
			notify()
		})
		m.updateMedia()
	}
	m.restoreSession()
	requests := make(chan designCommand)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(requests)
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			var c designCommand
			if json.Unmarshal(scanner.Bytes(), &c) != nil {
				continue
			}
			select {
			case requests <- c:
			case <-done:
				return
			}
		}
	}()
	enc := json.NewEncoder(out)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-wake:
			m.wake()
		case c, ok := <-requests:
			if !ok || c.Op == "quit" {
				return nil
			}
			runDesignCommand(m, c)
			m.wake()
			if err := enc.Encode(designSnapshotWithWindow(m, quit)); err != nil {
				return err
			}
		case <-ticker.C:
			m.wake()
		}
	}
}
