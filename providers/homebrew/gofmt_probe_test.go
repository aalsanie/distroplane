package homebrew

import (
	"encoding/base64"
	"fmt"
	"go/format"
	"os"
	"testing"
)

func TestTemporaryJVMGofmtProbe(t *testing.T) {
	for _, name := range []string{"provider.go", "jvm_test.go", "jvm_brew_darwin_test.go"} {
		raw, err := os.ReadFile(name)
		if err != nil { t.Fatal(err) }
		result, err := format.Source(raw)
		if err != nil { t.Fatal(err) }
		fmt.Printf("JVM_GO_FORMAT:%s:%s\n", name, base64.StdEncoding.EncodeToString(result))
	}
	t.Fatal("formatting probe complete")
}
