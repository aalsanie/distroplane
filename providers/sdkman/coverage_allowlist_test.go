package sdkman

import (
	"strings"
	"testing"
)

func TestStateDistributionAllowlistAndLegacyPlatformNormalization(t *testing.T) {
	for _, value := range []string{
		"BISHENG", "CORRETTO", "ELIYA", "GRAALCE", "GRAALVM",
		"JETBRAINS", "KONA", "LIBERICA", "LIBERICA_NIK", "MANDREL",
		"MICROSOFT", "OPENJDK", "ORACLE", "SAP_MACHINE", "SEMERU",
		"TEMURIN", "ZULU",
	} {
		if !validStateDistribution(value) || !validStateDistributionOptional(value) {
			t.Errorf("documented State API distribution rejected: %s", value)
		}
	}
	for _, value := range []string{"", "UNSUPPORTED", "temurin", "../TEMURIN", "FUTURE"} {
		if validStateDistribution(value) {
			t.Errorf("unapproved SDKMAN vendor distribution accepted: %q", value)
		}
	}
	platforms := []struct{ legacy, canonical, stateID string }{
		{"LINUX_64", "LINUX_X64", "linuxx64"},
		{"LINUX_32", "LINUX_X32", "linuxx32"},
		{"MAC_OSX", "MAC_X64", "darwinx64"},
		{"WINDOWS_64", "WINDOWS_X64", "windowsx64"},
	}
	for _, p := range platforms {
		if got := canonicalPlatform(p.legacy); got != p.canonical {
			t.Errorf("%s canonicalized to %q", p.legacy, got)
		}
		if got := statePlatform(p.legacy, AuthState); got != p.canonical {
			t.Errorf("%s State API identity=%q", p.legacy, got)
		}
		if got := statePlatform(p.legacy, AuthLegacy); got != p.stateID {
			t.Errorf("%s legacy identity=%q", p.legacy, got)
		}
	}
	if !strings.HasPrefix(statePlatform("UNIVERSAL", AuthLegacy), "universal") {
		t.Fatal("universal legacy platform mapping lost")
	}
}
