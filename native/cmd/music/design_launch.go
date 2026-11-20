package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tinglan/native/internal/icons"
)

//go:embed designui/app.py designui/live.py designui/live_art.py designui/live_collections.py designui/live_settings.py designui/icons/*.svg gtk_collection.css
var designUIAssets embed.FS

// Preserve the diagnostic and backend switches of the old Go entry, but make
// an ordinary Linux launch open the integrated design window.
func useDesignUI(args []string, headless bool) bool {
	if headless {
		return false
	}
	for _, arg := range args {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch name {
		case "design-stdio", "demo", "fixture-dir", "profile", "audio-fixture", "check-background":
			return false
		}
	}
	return true
}

func unpackDesignUI(dir string) error {
	if err := fs.WalkDir(designUIAssets, "designui", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "designui" {
			return nil
		}
		path := filepath.Join(dir, strings.TrimPrefix(name, "designui/"))
		if entry.IsDir() {
			return os.Mkdir(path, 0700)
		}
		data, err := designUIAssets.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0600)
	}); err != nil {
		return err
	}
	css, err := designUIAssets.ReadFile("gtk_collection.css")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "gtk_collection.css"), css, 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "icon.png"), icons.AppPNG, 0600)
}

func launchDesignUI(args []string) error {
	backend, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "tinglan-gtk-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err = unpackDesignUI(dir); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	argv := append([]string{filepath.Join(dir, "app.py")}, args...)
	argv = append(argv, "--backend", backend)
	cmd := exec.CommandContext(ctx, "/usr/bin/python3", argv...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 4 * time.Second
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil && !errors.Is(ctx.Err(), context.Canceled) {
		return err
	}
	return nil
}
