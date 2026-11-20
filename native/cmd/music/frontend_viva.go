//go:build !linux || !gtk || !cgo

package main

const experimentalFrontend = false

func validateFrontend(int, string, bool) error { return nil }

func runFrontend(m *model) error { return runMusic(m) }
