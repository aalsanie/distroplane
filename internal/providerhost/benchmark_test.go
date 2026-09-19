package providerhost

import (
	"context"
	"testing"
)

func BenchmarkProviderProcessStartup(b *testing.B) {
	client := helperClient(b, "normal", nil)
	endpoint := helperEndpoint(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.Describe(context.Background(), endpoint); err != nil {
			b.Fatal(err)
		}
	}
}
