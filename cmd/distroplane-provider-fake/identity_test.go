package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestProviderNameFromExecutable(t *testing.T) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	if got := providerName(filepath.Join("tmp", "distroplane-provider-alpha"+suffix)); got != "alpha" {
		t.Fatalf("got=%q", got)
	}
	if got := providerName(filepath.Join("tmp", "other"+suffix)); got != "fake" {
		t.Fatalf("got=%q", got)
	}
}
