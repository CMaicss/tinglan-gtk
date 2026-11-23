//go:build linux

package media

import (
	"github.com/godbus/dbus/v5"
	"os"
	"testing"
	"time"
)

func TestMPRISWithIsolatedBus(t *testing.T) {
	if os.Getenv("HWMUSIC_TEST_MPRIS") == "" {
		t.Skip("run with dbus-run-session and HWMUSIC_TEST_MPRIS=1")
	}
	events := make(chan Event, 16)
	c := New(func(e Event) { events <- e })
	if c == nil {
		t.Fatal("MPRIS registration failed")
	}
	defer c.Close()
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	object := conn.Object(busName, objectPath)
	for key, want := range map[string]string{"Identity": "听澜", "DesktopEntry": "org.tinglan.unofficial.gtk"} {
		v, err := object.GetProperty("org.mpris.MediaPlayer2." + key)
		if err != nil || v.Value() != want {
			t.Fatalf("%s: got %v (%v), want %q", key, v, err, want)
		}
	}
	c.Update("synthetic", "fixture", 100, 12, true)
	v, err := object.GetProperty(playerInterface + ".PlaybackStatus")
	if err != nil || v.Value() != "Playing" {
		t.Fatal("playing state missing")
	}
	if err = object.Call(playerInterface+".Seek", 0, int64(3e6)).Err; err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		if e.Command != 5 || e.Position != 15 {
			t.Fatal("seek mapping incorrect")
		}
	case <-time.After(time.Second):
		t.Fatal("seek timed out")
	}
	c.Preferences(.6, true, "all")
	v, err = object.GetProperty(playerInterface + ".LoopStatus")
	if err != nil || v.Value() != "Playlist" {
		t.Fatal("loop state missing")
	}
	err = object.Call("org.freedesktop.DBus.Properties.Set", 0, playerInterface, "Volume", dbus.MakeVariant(.25)).Err
	if err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		if e.Command != 8 || e.Position != .25 {
			t.Fatal("volume mapping incorrect")
		}
	case <-time.After(time.Second):
		t.Fatal("volume timed out")
	}
	for _, art := range []string{"file:///fixture/cover.png", ""} {
		c.Update("synthetic", "fixture", 100, 12, true, art)
		v, err := object.GetProperty(playerInterface + ".Metadata")
		if err != nil {
			t.Fatal(err)
		}
		got, present := v.Value().(map[string]dbus.Variant)["mpris:artUrl"]
		if !present || got.Value() != art {
			t.Fatalf("artwork update %q: got %v, present=%t", art, got, present)
		}
	}
	c.Close()
	if object.Call(playerInterface+".Play", 0).Err == nil {
		t.Fatal("closed bus name still callable")
	}
}
