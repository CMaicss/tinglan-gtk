package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	imagecolor "image/color"
	"image/png"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vivaui/viva/ui"
	"tinglan/native/internal/lyrics"
	"tinglan/native/internal/music"
	"tinglan/native/internal/resource"
	"tinglan/native/internal/storage"
)

const designArtSide = 192

type designArtReply struct {
	Field, ID string
	Index     int
	PNG       []byte `json:",omitempty"`
}

type designArtView struct {
	Epoch, CacheRevision uint64
	Clearing             bool
	Pending              bool
	Reply                *designArtReply `json:",omitempty"`
	StageUpgrade         *designArtReply `json:",omitempty"`
	StageIdentity        string          `json:",omitempty"`
	LyricToken           string          `json:",omitempty"`
	LyricOffset          float64
	LyricFeedback        string `json:",omitempty"`
}

// Model access stays on the bridge loop. Only the worker's immutable inputs
// and its single-result channel cross goroutines. The bridge must defer
// designArtClose after deferring m.close, so no worker outlives the profile.
var designArtSessions sync.Map // *model -> *designArtSession

type designArtSession struct {
	result        chan []byte
	clear         bool
	revision      uint64
	cancel        context.CancelFunc
	request       designCommand
	epoch         uint64
	urls          []string
	reply         *designArtReply
	stageStore    *storage.Store
	stageJob      *designStageJob
	stageNext     *designStageJob
	stageDownload *designStageDownload
	feedbackToken string
	feedback      string
}

func designArtFor(m *model) *designArtSession {
	if s, ok := designArtSessions.Load(m); ok {
		return s.(*designArtSession)
	}
	s := &designArtSession{}
	designArtSessions.Store(m, s)
	return s
}

func designArtClose(m *model) {
	if value, ok := designArtSessions.LoadAndDelete(m); ok {
		s := value.(*designArtSession)
		if s.result != nil {
			s.cancel()
			<-s.result
		}
		if d := s.stageDownload; d != nil {
			d.cancel()
			<-d.done
		}
	}
}

func designArtCommand(m *model, c designCommand) bool {
	switch c.Op {
	case "art-clear":
		if c.Index != 1 || m.store == nil {
			return true
		}
		s := designArtFor(m)
		s.clear, s.reply = true, nil
		m.mediaArt.clearing = true
		m.mediaArt.invalidate()
		m.updateMedia()
		if s.cancel != nil {
			s.cancel()
		}
		return true
	case "art":
		s := designArtFor(m)
		if s.clear || s.result != nil || s.reply != nil {
			return true // one request in flight, no work queue
		}
		reply := &designArtReply{Field: c.Text, ID: c.ID, Index: c.Index}
		urls := designArtURLs(m, c)
		stage := c.Text == "Stage"
		var job designStageJob
		if stage && c.Index == m.queue.Index && c.ID == m.qualityTrackKey() {
			job = designStageJobFor(m, c, urls)
		}
		if len(urls) == 0 && job.albumKey == "" {
			s.reply = reply
			return true
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		done := make(chan []byte, 1)
		s.result, s.cancel, s.request, s.epoch, s.urls = done, cancel, c, m.accountEpoch, urls
		client, store, offline := m.resourceClient, m.store, m.demo
		var cached ui.Bitmap
		for _, raw := range urls {
			if b := m.covers[raw]; b.Width > 0 {
				cached = b
				m.touchCover(raw)
				break
			}
		}
		if stage {
			if s.stageStore == nil {
				s.stageStore = designStageStore(store)
			}
			s.stageJob = &job
			stageStore := s.stageStore
			go func() {
				defer cancel()
				done <- designStageCached(ctx, stageStore, store, urls, job.albumKey, cached)
			}()
			return true
		}
		go func() {
			defer cancel()
			done <- designArtLoad(ctx, client, store, offline, urls, cached)
		}()
		return true
	case "lyric-offset", "lyric-import":
		token := designLyricToken(m)
		if token == "" || c.ID != token {
			return true
		}
		s := designArtFor(m)
		s.feedbackToken = token
		if c.Op == "lyric-import" {
			s.feedback = designImportLyrics(m, c.Text)
		} else if c.Index == -1 || c.Index == 1 {
			offset := max(-10, min(10, m.lyricOffset+float64(c.Index)*.5))
			track := m.queue.Current()
			if m.store != nil && m.store.SaveLyricOffset(m.trackOffsetKey(*track), offset) != nil {
				s.feedback = "歌词偏移保存失败"
			} else {
				m.lyricOffset = offset
				s.feedback = fmt.Sprintf("歌词偏移 %+.1f 秒", offset)
			}
		}
		return true
	}
	return false
}

func designArtState(m *model) any {
	v := designArtView{Epoch: m.accountEpoch, LyricToken: designLyricToken(m), LyricOffset: m.lyricOffset}
	s := designArtFor(m)
	c := designCommand{Text: "Stage", ID: m.qualityTrackKey(), Index: m.queue.Index}
	current := designStageJobFor(m, c, designArtURLs(m, c))
	v.StageIdentity = current.identity()
	if s.feedbackToken == v.LyricToken {
		v.LyricFeedback = s.feedback
	}
	if s.result != nil {
		if s.epoch != m.accountEpoch || (s.request.Text == "Stage" && (s.request.Index != m.queue.Index || s.request.ID != m.qualityTrackKey())) {
			s.cancel()
		}
		select {
		case data := <-s.result:
			c := s.request
			currentURLs := designArtURLs(m, c)
			valid := !s.clear && s.epoch == m.accountEpoch && slices.Equal(s.urls, currentURLs)
			if j := s.stageJob; j != nil {
				if valid && j.sameArt(current) {
					s.reply = &designArtReply{Field: c.Text, ID: c.ID, Index: c.Index, PNG: data}
					if active := s.stageDownload; designStageWidth(data) < designStageEnough && (active == nil || !active.sameArt(*j)) {
						s.stageNext = j
					}
				}
			} else {
				if !valid {
					data = nil
				}
				s.reply = &designArtReply{Field: c.Text, ID: c.ID, Index: c.Index, PNG: data}
			}
			s.stageJob = nil
			s.result, s.cancel, s.urls, s.request = nil, nil, nil, designCommand{}
		default:
			v.Pending = true
		}
	}
	v.StageUpgrade = s.stageState(m, current)
	if s.clear && s.result == nil && s.stageDownload == nil {
		s.clear, s.reply = false, nil
		s.revision++
		m.covers, m.stageCovers, m.coverUsed, m.coverRetry = nil, nil, nil, nil
		if s.stageStore == nil {
			s.stageStore = designStageStore(m.store)
		}
		if err := clearDesignArt(m.store, s.stageStore); err != nil {
			m.setStatus("封面缓存未能全部清除")
		} else {
			m.mediaArt.attempted = false
			m.setStatus("封面缓存已清除")
		}
		m.mediaArt.clearing = false
		m.updateMedia()
	}
	v.CacheRevision, v.Clearing = s.revision, s.clear
	v.Reply, s.reply = s.reply, nil
	return v
}

// Resolve only a model-owned (surface, position, identity) tuple; never
// treat a command field as a URL. Lists are not searched or prefetched.
func designArtURLs(m *model, c designCommand) []string {
	var rows []music.Track
	switch c.Text {
	case "Favorites":
		rows = m.tracks
	case "Results":
		rows = m.filtered
	case "History":
		rows = m.localHistory
	case "Stage":
		if c.Index != m.queue.Index || c.ID != m.qualityTrackKey() {
			return nil
		}
		rows = m.queue.Tracks
	case "Queue":
		rows = m.queue.Tracks
	case "Detail":
		rows = m.localTracks
	case "Playlists":
		if c.Index >= 0 && c.Index < len(m.localPlaylists) {
			p := m.localPlaylists[c.Index]
			if p.ID == c.ID && p.ID != "" && m.resourceAllowed(p.CoverURL) {
				return []string{p.CoverURL}
			}
		}
		return nil
	}
	if c.Index < 0 || c.Index >= len(rows) {
		return nil
	}
	t := rows[c.Index]
	if t.ID == "" || string(t.Source)+":"+t.ID != c.ID {
		return nil
	}
	var urls []string
	add := func(raw string) {
		if raw != "" && len(raw) <= 8192 && !slices.Contains(urls, raw) && m.resourceAllowed(raw) {
			urls = append(urls, raw)
		}
	}
	add(t.CoverURL)
	for _, raw := range t.CoverURLs[:min(4, len(t.CoverURLs))] {
		add(raw)
	}
	if t.Album != nil {
		add(t.Album.CoverURL)
		add(m.albumCovers["album:"+t.Album.ID])
	}
	return urls
}

func designArtLoad(ctx context.Context, client *resource.Client, store *storage.Store, offline bool, urls []string, cached ui.Bitmap) []byte {
	if cached.Width > 0 && cached.Height > 0 {
		img := image.NewRGBA(image.Rect(0, 0, cached.Width, cached.Height))
		for y := 0; y < cached.Height; y++ {
			for x := 0; x < cached.Width; x++ {
				i := y*cached.Stride + x*4
				img.SetRGBA(x, y, imagecolor.RGBA{R: cached.Pix[i+2], G: cached.Pix[i+1], B: cached.Pix[i], A: cached.Pix[i+3]})
			}
		}
		return designArtPNG(img)
	}
	for _, raw := range urls {
		if ctx.Err() != nil {
			break
		}
		if store != nil {
			if data, err := store.LoadCover(raw); err == nil {
				if png := designArtDecode(data); len(png) > 0 {
					return png
				}
			}
		}
	}
	for _, raw := range urls {
		if ctx.Err() != nil {
			break
		}
		if !offline && client != nil {
			data, err := client.Bytes(ctx, raw, 8*1024*1024)
			if err != nil {
				continue
			}
			if png := designArtDecode(data); len(png) > 0 {
				if store != nil && ctx.Err() == nil {
					_ = store.SaveCover(raw, png)
				}
				return png
			}
		}
	}
	return nil
}

func designArtDecode(data []byte) []byte {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return designArtPNG(img)
}

func designArtPNG(img image.Image) []byte {
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	n := min(side, designArtSide)
	ox, oy := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	out := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			out.Set(x, y, img.At(ox+x*side/n, oy+y*side/n))
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if enc.Encode(&buf, out) != nil {
		return nil
	}
	return buf.Bytes()
}

func designLyricToken(m *model) string {
	if t := m.queue.Current(); t != nil && !m.resolving {
		return fmt.Sprintf("%d/%d/%s:%s", m.accountEpoch, m.playbackGeneration, t.Source, t.ID)
	}
	return ""
}

func designImportLyrics(m *model, path string) string {
	const limit = 512 * 1024
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return "歌词文件不可用或超过 512KB"
	}
	// Use non-blocking open on the design frontend's Linux hosts, including
	// if the selected regular file is replaced with a FIFO before open.
	flags := os.O_RDONLY
	switch runtime.GOOS {
	case "linux":
		flags |= 0x800 // O_NONBLOCK
	case "darwin":
		flags |= 0x4 // O_NONBLOCK
	}
	f, err := os.OpenFile(path, flags, 0)
	if err != nil {
		return "无法读取歌词文件"
	}
	defer f.Close()
	if stat, err := f.Stat(); err != nil || !os.SameFile(info, stat) {
		return "歌词文件已更改，请重新选择"
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(data) > limit {
		return "歌词文件不可用或超过 512KB"
	}
	text, err := lyrics.Decode(data)
	if err != nil {
		return "歌词编码无法识别"
	}
	lines, _ := lyrics.ParseAny(text)
	if len(lines) > 5000 {
		return "歌词超过 5000 行"
	}
	if !slices.ContainsFunc(lines, func(line lyrics.Line) bool { return strings.TrimSpace(line.Text) != "" }) {
		return "歌词文件没有可用内容"
	}
	const local = "本地导入"
	if m.store != nil {
		key := m.trackLyricsKey(*m.queue.Current())
		if err := m.store.SaveLyrics(key, storage.LyricDocument{Text: text, Source: local}, true); err != nil {
			return "歌词保存失败"
		}
	}
	m.lyricLines, m.lyricSource = lines, local
	return "已导入歌词"
}
