package planner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func BenchmarkBuildTargets(b *testing.B) {
	d, _ := domain.NewSHA256Digest(strings.Repeat("a", 64))
	for _, count := range []int{1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("%d", count), func(b *testing.B) {
			targets := make([]string, count)
			for i := 0; i < count; i++ {
				targets[i] = fmt.Sprintf(`{"id":"t%04d","provider":{"name":"fake"},"configuration":{"n":%d}}`, i, i)
			}
			cfg, err := config.Decode(strings.NewReader(baseConfig(strings.Join(targets, ","))))
			if err != nil {
				b.Fatal(err)
			}
			c := fakeClient{describe: func(context.Context, Endpoint) (protocol.DescribeResponse, error) {
				return describe("1", protocol.CapabilityPlan), nil
			}, plan: func(context.Context, Endpoint, protocol.PlanRequest) (protocol.PlanResponse, error) {
				return protocol.PlanResponse{}, nil
			}}
			p, _ := New(c, Options{Resolver: fakeResolver{path: "/p"}, Hasher: fakeHasher{digest: d, size: 1}})
			loaded := config.Loaded{Config: cfg, BaseDir: "/work"}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := p.Build(context.Background(), loaded); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
func BenchmarkFileHasher1MiB(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "artifact")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1<<20)), 0o600); err != nil {
		b.Fatal(err)
	}
	h := FileHasher{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := h.Hash(path); err != nil {
			b.Fatal(err)
		}
	}
}
