package winget

import (
	"encoding/base64"
	"fmt"
	"go/format"
	"os"
	"testing"
)

func TestTemporaryWinGetFormattingProbe(t *testing.T) {
	for _, path := range []string{"zip_test.go", "zip_windows_test.go"} {
		raw, err := os.ReadFile(path)
		if err != nil { t.Fatal(err) }
		formatted, err := format.Source(raw)
		if err != nil { t.Fatal(err) }
		fmt.Printf("WINGET_FORMAT:%s:%s\n", path, base64.StdEncoding.EncodeToString(formatted))
	}
	t.Fatal("format output captured")
}
