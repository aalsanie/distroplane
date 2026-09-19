package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func BenchmarkColdProcessStartup(b *testing.B) {
	binary := filepath.Join(b.TempDir(), "distroplane")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		b.Fatalf("build benchmark binary: %v: %s", err, output)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		command := exec.Command(binary, "version", "--json")
		if err := command.Run(); err != nil {
			b.Fatal(err)
		}
	}
}
