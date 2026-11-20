package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tinglan/native/internal/plugin"
	"tinglan/native/internal/source"
	"tinglan/native/internal/storage"
)

func TestDesignSettingsPluginLifecycle(t *testing.T) {
	dir := t.TempDir()
	fixture := "../../internal/plugin/testdata/sample-plugin"
	manifest, err := os.ReadFile(filepath.Join(fixture, "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(dir, "bin", "sample"), fixture).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	m := newModel()
	profile := t.TempDir()
	m.store, m.registry, m.settings = storage.New(profile), source.NewRegistry(), storage.Defaults()
	m.plugins = plugin.NewManager(filepath.Join(profile, "sources"), m.store, nil)
	t.Cleanup(m.close)
	t.Cleanup(func() { designSettingsClose(m) })
	act := func(op, id, text string, confirmed int) string {
		t.Helper()
		if !designSettingsCommand(m, designCommand{Op: op, ID: id, Text: text, Index: confirmed}) {
			t.Fatalf("unhandled command: %s", op)
		}
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			state, err := json.Marshal(designSettingsState(m))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(state), `"SourceBusy":true`) {
				return string(state)
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("source operation did not finish")
		return ""
	}
	act("settings-source-install", "", dir, 0)
	if len(m.plugins.List()) != 0 {
		t.Fatal("ran unconfirmed import")
	}
	state := act("settings-source-install", "", dir, 1)
	entries := m.plugins.List()
	if len(entries) != 1 || m.plugins.Running() != 0 || !strings.Contains(state, entries[0].Name()) {
		t.Fatalf("probe did not discover a disabled fixture: %s", state)
	}
	id := entries[0].ID()
	act("settings-source-enable", string(id), "", 0)
	if m.plugins.Running() != 0 || m.plugins.Consented(id) {
		t.Fatal("enabled without confirmation")
	}
	act("settings-source-enable", string(id), "", 1)
	src, ok := m.registry.Source(id)
	if !ok {
		t.Fatal("enabled fixture not in registry")
	}
	page, err := src.Catalog().Search(context.Background(), source.SearchRequest{Query: "x", Limit: 5})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("fixture search: %v %+v", err, page.Items)
	}
	act("settings-source-disable", string(id), "", 0)
	if _, ok := m.registry.Source(id); ok || m.plugins.Running() != 0 {
		t.Fatal("disabled fixture retained")
	}
	prefs, err := m.store.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Sources[len(prefs.Sources)-1].Enabled {
		t.Fatal("disabled state not persisted")
	}
	reopened := plugin.NewManager(m.plugins.Root(), storage.New(profile), nil)
	defer reopened.Close()
	if failures := reopened.LoadEnabled(context.Background(), source.NewRegistry()); len(failures) != 0 || reopened.Running() != 0 {
		t.Fatalf("disabled external source started after restart: %v", failures)
	}
}

func TestDesignSettingsPersistPlaybackAndAppearance(t *testing.T) {
	m := newModel()
	m.store, m.settings = storage.New(t.TempDir()), storage.Defaults()
	t.Cleanup(m.close)
	t.Cleanup(func() { designSettingsClose(m) })
	for _, c := range []designCommand{
		{Op: "settings-quality", Index: 1},
		{Op: "settings-mode", Index: 3},
		{Op: "settings-theme", Index: 2},
		{Op: "settings-font-size", Index: 117},
		{Op: "settings-lyrics-match", Index: 0},
	} {
		if !designSettingsCommand(m, c) {
			t.Fatalf("unhandled command: %s", c.Op)
		}
	}
	prefs, err := m.store.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Quality != "lossless" || !prefs.Shuffle || prefs.RepeatMode != "off" || prefs.ThirdPartyLyrics ||
		prefs.Appearance.Theme != "graphite" || prefs.Appearance.FontScale != 1.17 {
		t.Fatalf("incorrect persisted settings: %+v", prefs)
	}
}
