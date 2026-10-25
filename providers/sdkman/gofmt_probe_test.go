package sdkman

import (
	"encoding/base64"
	"fmt"
	"go/format"
	"os"
	"testing"
)

func TestTemporaryFormatProbe(t *testing.T) {
	for _, path := range []string{"provider.go", "api.go", "state_api_test.go"} {
		raw, err := os.ReadFile(path)
		if err != nil { t.Fatal(err) }
		formatted, err := format.Source(raw)
		if err != nil { t.Fatal(err) }
		fmt.Printf("SDKMAN_FORMAT_PROBE:%s:%s\n", path, base64.StdEncoding.EncodeToString(formatted))
	}
	t.Fatal("format probe output complete")
}
