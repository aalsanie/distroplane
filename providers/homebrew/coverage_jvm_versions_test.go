package homebrew

import (
	"testing"
)

func TestJavaRuntimeAllowlistAndJVMLauncherValidation(t *testing.T) {
	for _, version := range []string{"8", "11", "17", "21", "25"} {
		if !supportedJavaVersion(version) {
			t.Errorf("supported runtime rejected: %s", version)
		}
	}
	for _, version := range []string{"", "7", "26", "21-ea"} {
		if supportedJavaVersion(version) {
			t.Errorf("unsupported runtime accepted: %q", version)
		}
	}
	for _, name := range []string{"java", "java21", "sample_tool", "bin-name", "Java.Tool"} {
		if !validJVMDistBinary(name) {
			t.Errorf("safe JVM launcher rejected: %s", name)
		}
	}
	for _, name := range []string{"", ".", "..", "unsafe/name", "has space", "bad\x00name"} {
		if validJVMDistBinary(name) {
			t.Errorf("unsafe JVM launcher accepted: %q", name)
		}
	}
}
