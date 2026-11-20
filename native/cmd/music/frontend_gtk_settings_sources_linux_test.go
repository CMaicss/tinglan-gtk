//go:build linux && gtk && cgo

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tinglan/native/internal/plugin"
	"tinglan/native/internal/source"
	"tinglan/native/internal/storage"
)

func TestGTKSettingsSourceConsentLifecycle(t *testing.T) {
	dir := t.TempDir()
	fixture, err := os.ReadFile("../../internal/plugin/testdata/sample-plugin/source.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.json"), fixture, 0600); err != nil {
		t.Fatal(err)
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		goPath = filepath.Join(os.Getenv("GOROOT"), "bin", "go")
	}
	cmd := exec.Command(goPath, "build", "-o", filepath.Join(dir, "bin", "sample"), "../../internal/plugin/testdata/sample-plugin")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build sample: %v\n%s", err, out)
	}
	profile := t.TempDir()
	store := storage.New(profile)
	manager := plugin.NewManager(filepath.Join(profile, "sources"), store, nil)
	t.Cleanup(manager.Close)
	registry := source.NewRegistry()
	ctx := context.Background()
	m := newModel()
	m.store, m.registry, m.plugins = store, registry, manager
	if _, err := gtkInstallSource(ctx, m, dir, false); err == nil || len(manager.List()) != 0 {
		t.Fatal("import ran without probe consent")
	}
	entry, err := gtkInstallSource(ctx, m, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name() == "sample" || manager.Running() != 0 || manager.Consented(entry.ID()) {
		t.Fatalf("probe must discover metadata without enabling a plugin: %+v", entry)
	}
	if err := gtkEnableSource(ctx, manager, registry, entry.ID(), false); err == nil || manager.Running() != 0 {
		t.Fatal("enabled without consent")
	}
	if err := gtkEnableSource(ctx, manager, registry, entry.ID(), true); err != nil {
		t.Fatal(err)
	}
	src, ok := registry.Source(entry.ID())
	if !ok {
		t.Fatal("enabled source not registered")
	}
	page, err := src.Catalog().Search(ctx, source.SearchRequest{Query: "x", Limit: 5})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("fixture search: %v %+v", err, page.Items)
	}
	if err := manager.Disable(registry, entry.ID()); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Source(entry.ID()); ok || manager.Running() != 0 {
		t.Fatal("disabled source still available")
	}
	reopened := plugin.NewManager(manager.Root(), store, nil)
	t.Cleanup(reopened.Close)
	if failures := reopened.LoadEnabled(ctx, registry); len(failures) != 0 || reopened.Running() != 0 || !reopened.Consented(entry.ID()) {
		t.Fatalf("disable/consent not persisted: %v", failures)
	}
}
