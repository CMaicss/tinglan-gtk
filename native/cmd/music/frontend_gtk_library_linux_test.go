//go:build linux && gtk && cgo

package main

import (
	"context"
	"testing"

	"tinglan/native/internal/music"
)

func TestGTKLibraryPlayPreservesViewAndReplacesQueue(t *testing.T) {
	m := newModel()
	m.demo, m.view = true, 2
	m.queue.Replace([]music.Track{{ID: "old", Title: "Old"}}, 0)
	tracks := []music.Track{{ID: "a", Title: "A"}, {ID: "b", Title: "B", OnlySource: music.SourceHuawei}}
	gtkLibraryPlay(m, tracks, 1)
	if m.view != 2 || m.queue.Index != 1 || len(m.queue.Tracks) != 2 || m.queue.Current().ID != "b" || m.queue.Current().OnlySource != music.SourceHuawei {
		t.Fatalf("view=%d queue=%+v", m.view, m.queue)
	}
	gtkLibraryPlay(m, tracks, 2)
	if m.queue.Index != 1 {
		t.Fatal("invalid activation altered the queue")
	}
	t.Cleanup(m.close)
}

func TestGTKLibraryFavoriteTogglePreservesPlaylistMember(t *testing.T) {
	m := newModel()
	openProfileForTest(t, m, t.TempDir())
	t.Cleanup(m.close)
	ctx := context.Background()
	raw := music.Track{ID: "library-track", Source: music.SourceHuawei, Title: "Library Track"}
	id, err := m.local.AddToLibrary(ctx, raw, nil, music.MatchPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	pl, err := m.local.CreatePlaylist(ctx, "Playlist", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.local.AddToPlaylist(ctx, pl.ID, id); err != nil {
		t.Fatal(err)
	}
	m.openPlaylist(pl)
	for _, want := range []bool{true, false} {
		gtkLibraryToggleFavorite(m, m.localTracks[0])
		row, ok := m.local.Row(id)
		if !ok || row.Favorite != want || (len(m.tracks) != 0) != want {
			t.Fatalf("favorite=%v row=%+v", want, row)
		}
	}
	ids, err := m.local.PlaylistTracks(ctx, pl.ID)
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("members=%v err=%v", ids, err)
	}
}

func TestGTKLibraryImportedPlaylistOrdersAndRestrictsQueue(t *testing.T) {
	m := newModel()
	openProfileForTest(t, m, t.TempDir())
	ctx := context.Background()
	var ids []string
	for _, title := range []string{"First Member", "Second Member"} {
		id, err := m.local.AddToLibrary(ctx, music.Track{ID: title, Source: music.SourceHuawei, Title: title}, nil, music.MatchPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	p, err := m.local.EnsureImportedPlaylist(ctx, music.SourceHuawei, "remote-list", "Imported", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.local.ReplaceImportedMembers(ctx, p.ID, ids, p.Name, "", ""); err != nil {
		t.Fatal(err)
	}
	m.refreshPlaylists()
	l := gtkLibrary{model: m, playlists: m.localPlaylists, playlistPage: gtkLibraryPlaylists}
	l.action(gtkLibraryPlaylists, gtkLibOpen, 0)
	if m.localDetail == nil || m.localDetail.ID != p.ID || l.playlistPage != gtkLibraryDetail {
		t.Fatal("the playlist card did not open its shared model detail")
	}
	l.detailTracks = m.localTracks
	l.action(gtkLibraryDetail, gtkLibRemove, 0)
	l.action(gtkLibraryDetail, gtkLibPlay, 1)
	if len(m.queue.Tracks) != 2 || m.queue.Index != 1 || m.queue.Tracks[0].ID != ids[0] || m.queue.Current().ID != ids[1] || m.queue.Current().OnlySource != music.SourceHuawei {
		t.Fatalf("unexpected imported playlist queue: %+v", m.queue)
	}
}
