package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"path/filepath"

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

func clearDesignArt(store *storage.Store) error {
	return errors.Join(store.ClearCovers(), designStageStore(store).ClearCovers())
}

type designStageFetch func(context.Context, string) ([]byte, error)

// A separate cache keeps a list thumbnail from satisfying the HD request.
func designStageLoad(ctx context.Context, store *storage.Store, urls []string, fetch designStageFetch, album func(context.Context) string, albumKey string) []byte {
	var best []byte
	var side int
	seen := map[string]bool{}
	read := func(key string) bool {
		if store == nil || key == "" {
			return false
		}
		data, err := store.LoadCover(key)
		if err != nil {
			return false
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != cfg.Height || cfg.Width > designStageSide {
			return false
		}
		if cfg.Width > side {
			best, side = data, cfg.Width
		}
		return true
	}
	get := func(raw, key string) {
		if fetch == nil || raw == "" || seen[raw] || ctx.Err() != nil {
			return
		}
		seen[raw] = true
		data, err := fetch(ctx, raw)
		if err != nil || ctx.Err() != nil {
			return
		}
		data, n := designStageEncode(data)
		if n <= side || ctx.Err() != nil {
			return
		}
		best, side = data, n
		if store != nil {
			_ = store.SaveCover(key, data)
		}
	}
	for _, raw := range urls {
		// A small cached image is a fallback, not a reason to skip its URL.
		read(raw)
		if side >= designStageEnough {
			return best
		}
	}
	if read(albumKey) && side >= designStageEnough {
		return best
	}
	for _, raw := range urls {
		get(raw, raw)
		if side >= designStageEnough {
			return best
		}
	}
	if album != nil && ctx.Err() == nil {
		get(album(ctx), albumKey)
	}
	return best
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
	if m.demo || m.registry == nil || index != m.queue.Index || m.queue.Current() == nil {
		return nil, ""
	}
	track := *m.queue.Current()
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
	if track.Album == nil || track.Album.ID == "" {
		return nil, ""
	}
	src, ok := m.registry.Source(track.Source)
	if !ok || src.Catalog() == nil {
		return nil, ""
	}
	catalog := src.Catalog()
	ref := source.EntityRef{Source: track.Source, Kind: "album", ID: track.Album.ID}
	return func(ctx context.Context) string {
		detail, err := catalog.AlbumDetail(ctx, source.EntityRequest{Ref: ref, Limit: 1})
		if err != nil {
			return ""
		}
		return detail.Album.CoverURL
	}, "album:" + string(ref.Source) + ":" + ref.ID
}

func designStageNetwork(client *resource.Client, offline bool) designStageFetch {
	if offline || client == nil {
		return nil
	}
	return func(ctx context.Context, raw string) ([]byte, error) { return client.Bytes(ctx, raw, 8*1024*1024) }
}
