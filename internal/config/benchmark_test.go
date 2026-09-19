package config

import (
	"bytes"
	"testing"
)

func BenchmarkDecodeConfig(b *testing.B) {
	raw := []byte(`{"schemaVersion":"1","release":{"id":"v1","artifacts":[{"name":"app","source":"dist/app","mediaType":"application/octet-stream"}]},"targets":[{"id":"npm","provider":{"name":"npm","executable":"/usr/local/bin/distroplane-provider-npm"},"configuration":{"package":"example","registry":"https://registry.example.test"}}]}`)
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Decode(bytes.NewReader(raw)); err != nil {
			b.Fatal(err)
		}
	}
}
