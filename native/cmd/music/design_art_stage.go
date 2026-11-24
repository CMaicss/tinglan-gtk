package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"path/filepath"
	"slices"
	"time"

	"github.com/vivaui/viva/ui"

	"golang.org/x/image/draw"
	"tinglan/native/internal/resource"
	"tinglan/native/internal/source"
	"tinglan/native/internal/storage"
)

const designStageSide = 848 // 424 logical pixels on a 2× display
const designStageEnough = 640
const designStageBytes = 256 * 1024 // existing cover slot limit

func designStageStore(store *storage.Store) *storage.Store {
	if store == nil {
		return nil
	}
	return storage.New(filepath.Join(store.Dir(), "stage-art"))
}

func clearDesignArt(store, stage *storage.Store) error {
	return errors.Join(store.ClearCovers(), stage.ClearCovers())
}

type designStageFetch func(context.Context, string) ([]byte, error)

// Memory/disk lookup and HD downloads run independently: a previous track's
// download cannot hold up the current track's cached cover.
type designStageJob struct {
	request  designCommand
	epoch    uint64
	urls     []string
	album    func(context.Context) string
	albumKey string
}

type designStageDownload struct {
	designStageJob
	done   chan []byte
	cancel context.CancelFunc
}

func designStageJobFor(m *model, c designCommand, urls []string) designStageJob {
	album, key := designStageAlbum(m, c.Index)
	return designStageJob{request: c, epoch: m.accountEpoch, urls: urls, album: album, albumKey: key}
}

func (j designStageJob) sameArt(other designStageJob) bool {
	return j.epoch == other.epoch && j.request.ID == other.request.ID && j.albumKey == other.albumKey && slices.Equal(j.urls, other.urls)
}

func (j designStageJob) identity() string {
	if j.request.ID == "" {
		return ""
	}
	// Keep provider URLs and album keys on the Go side of the bridge.
	data, _ := json.Marshal(append([]string{j.albumKey}, j.urls...))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *designArtSession) stageState(m *model, current designStageJob) *designArtReply {
	var reply *designArtReply
	if active := s.stageDownload; active != nil {
		if s.clear || active.epoch != m.accountEpoch {
			active.cancel()
		}
		select {
		case data := <-active.done:
			s.stageDownload = nil
			if !s.clear && len(data) > 0 && active.sameArt(current) {
				reply = &designArtReply{Field: "Stage", ID: active.request.ID, Index: m.queue.Index, PNG: data}
			}
		default:
		}
	}
	if job := s.stageNext; job != nil {
		switch {
		case s.clear || !job.sameArt(current):
			s.stageNext = nil
		case s.stageDownload == nil:
			s.stageNext = nil
			fetch := designStageNetwork(m.resourceClient, m.demo)
			if fetch != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				d := &designStageDownload{designStageJob: *job, done: make(chan []byte, 1), cancel: cancel}
				s.stageDownload = d
				store := s.stageStore
				go func() {
					defer cancel()
					d.done <- designStageFetchHD(ctx, store, d.urls, fetch, d.album, d.albumKey)
				}()
			}
		}
	}
	return reply
}

func designStageWidth(data []byte) int {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != cfg.Height || cfg.Width > designStageSide {
		return 0
	}
	return cfg.Width
}

func designStageDisk(ctx context.Context, stage *storage.Store, urls []string, albumKey string) []byte {
	var best []byte
	var width int
	if stage != nil {
		for _, key := range append(slices.Clone(urls), albumKey) {
			if key == "" || ctx.Err() != nil {
				continue
			}
			data, _ := stage.LoadCover(key)
			if n := designStageWidth(data); n > width {
				if n >= designStageEnough {
					return data
				}
				best, width = data, n // older builds cached small images here
			}
		}
	}
	return best
}

func designStageCached(ctx context.Context, stage, thumbnails *storage.Store, urls []string, albumKey string, bitmap ui.Bitmap) []byte {
	best := designStageDisk(ctx, stage, urls, albumKey)
	width := designStageWidth(best)
	if width >= designStageEnough {
		return best
	}
	thumb := designArtLoad(ctx, nil, thumbnails, true, urls, bitmap)
	if designStageWidth(thumb) > width {
		return thumb
	}
	return best
}

func designStageFetchHD(ctx context.Context, store *storage.Store, urls []string, fetch designStageFetch, album func(context.Context) string, albumKey string) []byte {
	// Another track may have filled the same cover slot while this job waited.
	if data := designStageDisk(ctx, store, urls, albumKey); designStageWidth(data) >= designStageEnough {
		return data
	}
	get := func(raw, key string) []byte {
		if raw == "" || ctx.Err() != nil {
			return nil
		}
		data, err := fetch(ctx, raw)
		if err != nil || ctx.Err() != nil {
			return nil
		}
		data, n := designStageEncode(data)
		if n < designStageEnough || ctx.Err() != nil {
			return nil
		}
		if store != nil {
			_ = store.SaveCover(key, data)
		}
		return data
	}
	for _, raw := range urls {
		if data := get(raw, raw); data != nil {
			return data
		}
	}
	if album != nil && ctx.Err() == nil {
		if raw := album(ctx); !slices.Contains(urls, raw) {
			return get(raw, albumKey)
		}
	}
	return nil
}

func designStageEncode(data []byte) ([]byte, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 {
		return nil, 0
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0
	}
	b := img.Bounds()
	rawSide := min(b.Dx(), b.Dy())
	side := min(rawSide, designStageSide)
	x, y := b.Min.X+(b.Dx()-rawSide)/2, b.Min.Y+(b.Dy()-rawSide)/2
	out := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.CatmullRom.Scale(out, out.Bounds(), img, image.Rect(x, y, x+rawSide, y+rawSide), draw.Src, nil)
	// The wire field is historically named PNG; GTK decodes by content.
	for _, quality := range []int{92, 85, 76, 65, 50} {
		var buf bytes.Buffer
		if jpeg.Encode(&buf, out, &jpeg.Options{Quality: quality}) == nil && buf.Len() <= designStageBytes {
			return buf.Bytes(), side
		}
	}
	return nil, 0
}

func designStageAlbum(m *model, index int) (func(context.Context) string, string) {
	trackPtr := m.queue.Current()
	if index != m.queue.Index || trackPtr == nil || trackPtr.Album == nil || trackPtr.Album.ID == "" {
		return nil, ""
	}
	track := *trackPtr
	// Projected canonical tracks may not name the provider. Use the binding
	// owning the displayed album rather than asking a different provider.
	if track.Source == "" {
		if row, ok := m.canonicalFor(track); ok {
			for _, link := range row.SortedLinks() {
				if link.Album != nil && track.Album != nil && link.Album.ID == track.Album.ID {
					track.Source = link.Source
					break
				}
			}
		}
	}
	ref := source.EntityRef{Source: track.Source, Kind: "album", ID: track.Album.ID}
	key := "album:" + string(ref.Source) + ":" + ref.ID
	if track.Source == "" {
		return nil, ""
	}
	if m.demo || m.registry == nil {
		return nil, key
	}
	src, ok := m.registry.Source(track.Source)
	if !ok {
		return nil, key
	}
	catalog := src.Catalog()
	if catalog == nil {
		return nil, key
	}
	return func(ctx context.Context) string {
		detail, err := catalog.AlbumDetail(ctx, source.EntityRequest{Ref: ref, Limit: 1})
		if err != nil {
			return ""
		}
		return detail.Album.CoverURL
	}, key
}

func designStageNetwork(client *resource.Client, offline bool) designStageFetch {
	if offline || client == nil {
		return nil
	}
	return func(ctx context.Context, raw string) ([]byte, error) { return client.Bytes(ctx, raw, 8*1024*1024) }
}
