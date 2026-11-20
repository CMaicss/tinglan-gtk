package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"tinglan/native/internal/librarydb"
	"tinglan/native/internal/music"
	"tinglan/native/internal/source"
	"tinglan/native/internal/source/sourcetest"
)

func TestDesignCollectionsEditPersistsAndPreservesSearch(t *testing.T) {
	m := newModel()
	openProfileForTest(t, m, t.TempDir())
	m.openLocalLibrary()
	defer designCollectionClose(m)
	if m.local == nil {
		t.Fatal("library did not open")
	}
	ctx := context.Background()
	command := func(op, id, text string) {
		t.Helper()
		if !designCollectionCommand(m, designCommand{Op: op, ID: id, Text: text}) {
			t.Fatalf("unhandled command %s", op)
		}
	}
	command("collection-create", "", "  晚风  ")
	if m.localDetail == nil || m.localDetail.Name != "晚风" {
		t.Fatalf("create did not open new playlist: %+v", m.localDetail)
	}
	id := m.localDetail.ID
	m.filtered = []music.Track{{ID: "track", Source: music.SourceHuawei, Title: "Signal"}}
	m.searchQuery = "Signal"
	command("collection-add-Results", "huawei:stale", id)
	if len(m.localTracks) != 0 {
		t.Fatal("stale row changed playlist")
	}
	command("collection-add-Results", "huawei:track", id)
	if len(m.localTracks) != 1 || len(m.filtered) != 1 || m.filtered[0].ID != "track" {
		t.Fatalf("membership or active search lost: %+v / %+v", m.localTracks, m.filtered)
	}
	row, ok := m.local.RowForLink(music.SourceHuawei, "track")
	if !ok || row.Favorite {
		t.Fatal("playlist add should collect the track without favoriting it")
	}
	command("collection-favorite-Results", "huawei:track", "")
	command("collection-favorite-Results", "huawei:track", "")
	row, ok = m.local.RowForLink(music.SourceHuawei, "track")
	if !ok || row.Favorite || len(m.localTracks) != 1 {
		t.Fatal("unfavorite removed a playlist member")
	}
	command("collection-rename", id, "  晚风二  ")
	p, err := m.local.Playlist(ctx, id)
	if err != nil || p.Name != "晚风二" || p.TrackCount != 1 {
		t.Fatalf("unexpected persisted playlist: %+v, %v", p, err)
	}
	command("collection-remove-Detail", ":"+row.ID, id)
	members, err := m.local.PlaylistTracks(ctx, id)
	if err != nil || len(members) != 0 {
		t.Fatalf("membership not removed: %v, %v", members, err)
	}
	command("collection-delete", id, "")
	if _, err := m.local.Playlist(ctx, id); err == nil || m.localDetail != nil || len(m.localTracks) != 0 {
		t.Fatal("deleted playlist or detail still present")
	}
}

func TestDesignCollectionsMirrorsPagedSnapshotWithoutFavoriting(t *testing.T) {
	m := newModel()
	openProfileForTest(t, m, t.TempDir())
	m.openLocalLibrary()
	ctx := context.Background()
	provider := music.SourceHuawei
	track := func(id string) music.Track { return music.Track{ID: id, Source: provider, Title: "Fixture " + id} }
	fixture := &sourcetest.Fake{SourceID: provider, AuthStatus: source.AuthStatus{State: source.AuthSignedIn, AccountKey: "fixture-account"}, Pages: []source.ImportPage{
		{Next: "opaque-2", Tracks: []music.Track{track("alpha")}},
		{Tracks: []music.Track{track("beta")}},
	}}
	remote := music.Playlist{ID: "playlist-1", Name: "Fixture Playlist", Source: provider, Kind: music.PlaylistFavorite}
	id, err := designCollectionSync(ctx, m.local, fixture, "fixture-account", remote, librarydb.Playlist{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !reflect.DeepEqual(fixture.Calls, []string{"", "opaque-2"}) {
		t.Fatalf("wrong cursors: %v", fixture.Calls)
	}
	p, err := m.local.Playlist(ctx, id)
	if err != nil || p.Local() || p.TrackCount != 2 {
		t.Fatalf("wrong local copy: %+v %v", p, err)
	}
	a, aOK := m.local.RowForLink(provider, "alpha")
	b, bOK := m.local.RowForLink(provider, "beta")
	members, err := m.local.PlaylistTracks(ctx, id)
	if err != nil || !aOK || !bOK || a.Favorite || b.Favorite || !reflect.DeepEqual(members, []string{a.ID, b.ID}) {
		t.Fatalf("wrong membership or favorite state: %v, %v", members, err)
	}
	if _, err := designCollectionSync(ctx, m.local, fixture, "fixture-account", remote, p); !errors.Is(err, errRemoteEmpty) {
		t.Fatalf("empty update: %v", err)
	}
	kept, err := m.local.PlaylistTracks(ctx, id)
	if err != nil || !reflect.DeepEqual(kept, members) {
		t.Fatalf("empty update changed copy: %v %v", kept, err)
	}
	m.refreshPlaylists()
	for _, cmd := range []designCommand{{Op: "collection-rename", ID: id, Text: "Must not rename"}, {Op: "collection-delete", ID: id}} {
		designCollectionCommand(m, cmd)
	}
	defer designCollectionClose(m)
	if after, err := m.local.Playlist(ctx, id); err != nil || after.Name != p.Name {
		t.Fatalf("read-only guard failed: %+v %v", after, err)
	}
}

type designCollectionsCatalogFixture struct {
	source.CatalogSource
	calls []source.EntityRequest
}

func (f *designCollectionsCatalogFixture) ArtistDetail(_ context.Context, req source.EntityRequest) (source.ArtistDetail, error) {
	f.calls = append(f.calls, req)
	result := source.ArtistDetail{Artist: music.Artist{ID: req.Ref.ID, Name: "Fixture Artist"}}
	if req.Cursor == "" {
		result.Tracks = []music.Track{{ID: "first"}}
		result.NextTracks, result.HasMoreTracks = "next", true
	} else {
		result.Tracks = []music.Track{{ID: "second"}}
	}
	return result, nil
}

func TestDesignCollectionsArtistDetailRetainsSourceAndFollowsCursor(t *testing.T) {
	catalog := &designCollectionsCatalogFixture{}
	ref := source.EntityRef{Source: "fixture-provider", Kind: "artist", ID: "artist-1"}
	r := designCollectionReadEntity(context.Background(), catalog, ref)
	if r.err != nil || r.title != "Fixture Artist" || len(r.tracks) != 2 || r.tracks[1].ID != "second" {
		t.Fatalf("bad artist detail: %+v", r)
	}
	if len(catalog.calls) != 2 || catalog.calls[0].Ref != ref || catalog.calls[1].Ref != ref || catalog.calls[1].Cursor != "next" {
		t.Fatalf("wrong provider/id/cursor: %+v", catalog.calls)
	}
}
