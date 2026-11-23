package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/vivaui/viva/ui"
)

// The current artwork is the only file exported over MPRIS. Cover slot files
// aren't images (they contain a digest), and provider URLs may carry secrets.
// This state is owned by the model loop, not the artwork worker.
type mediaArtwork struct {
	epoch              uint64
	track              string
	urls               []string
	path, uri          string
	attempted, discard bool
	clearing           bool
	cancel             context.CancelFunc
	done               chan []byte
}

func (a *mediaArtwork) invalidate() {
	if a.cancel != nil {
		a.cancel()
		a.discard = true
	}
	if a.path != "" {
		_ = os.Remove(a.path)
		a.path, a.uri = "", ""
	}
}

func (a *mediaArtwork) close() {
	a.invalidate()
	if a.done != nil {
		<-a.done
		a.done, a.cancel = nil, nil
	}
}

func (a *mediaArtwork) publish(data []byte) {
	if len(data) == 0 {
		return
	}
	f, err := os.CreateTemp("", "tinglan-mpris-*.png")
	if err != nil {
		return
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	path, pathErr := filepath.Abs(f.Name())
	if writeErr != nil || closeErr != nil || pathErr != nil {
		_ = os.Remove(f.Name())
		return
	}
	a.path = path
	a.uri = (&url.URL{Scheme: "file", Path: path}).String()
}

func (m *model) updateMediaArtwork() string {
	a := &m.mediaArt
	track := m.qualityTrackKey()
	if runtime.GOOS != "linux" {
		a.track, a.epoch = track, m.accountEpoch
		return ""
	}
	var urls []string
	if track != "" {
		urls = designArtURLs(m, designCommand{Text: "Queue", ID: track, Index: m.queue.Index})
	}
	if track != a.track || a.epoch != m.accountEpoch || !slices.Equal(urls, a.urls) {
		a.invalidate()
		a.track, a.epoch, a.urls, a.attempted = track, m.accountEpoch, urls, false
	}
	if a.done != nil {
		select {
		case data := <-a.done:
			if !a.discard {
				a.publish(data)
			}
			a.done, a.cancel, a.discard = nil, nil, false
		default:
			return a.uri
		}
	}
	if a.clearing || a.attempted || len(urls) == 0 {
		return a.uri
	}
	a.attempted = true
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	done := make(chan []byte, 1)
	a.done, a.cancel = done, cancel
	client, store, offline := m.resourceClient, m.store, m.demo
	go func() {
		defer cancel()
		// Read existing thumbnails first. The fallback fetch does not write into
		// the profile, so it cannot repopulate slots during a user cache clear.
		data := designArtLoad(ctx, nil, store, true, urls, ui.Bitmap{})
		if len(data) == 0 {
			data = designArtLoad(ctx, client, nil, offline, urls, ui.Bitmap{})
		}
		done <- data
		m.requestUpdate()
	}()
	return a.uri
}
