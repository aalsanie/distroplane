package providerhost

import (
	"os"
	"testing"
)

// Preserve the caller's deliberately isolated environment in helper subprocesses.
// Branch instrumentation writes counters only in the parent test process.
func TestMain(m *testing.M) {
	if os.Getenv("GOBCO_STATS") == "" {
		m.Run()
		return
	}
	os.Exit(m.Run())
}
