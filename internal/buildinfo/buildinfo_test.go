package buildinfo

import "testing"

func TestCurrent(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = oldVersion, oldCommit, oldBuildDate })

	Version, Commit, BuildDate = "1.2.3", "abc123", "2026-09-17T00:00:00Z"
	got := Current()
	want := Info{Version: "1.2.3", Commit: "abc123", BuildDate: "2026-09-17T00:00:00Z"}
	if got != want {
		t.Fatalf("Current() = %#v, want %#v", got, want)
	}
}

func TestCurrentNormalizesEmptyValues(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = oldVersion, oldCommit, oldBuildDate })

	Version, Commit, BuildDate = "", "", ""
	got := Current()
	want := Info{Version: "unknown", Commit: "unknown", BuildDate: "unknown"}
	if got != want {
		t.Fatalf("Current() = %#v, want %#v", got, want)
	}
}

func TestInfoString(t *testing.T) {
	got := (Info{Version: "1.2.3", Commit: "abc123", BuildDate: "2026-09-17T00:00:00Z"}).String()
	want := "distroplane 1.2.3\ncommit: abc123\nbuilt: 2026-09-17T00:00:00Z"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestInfoStringNormalizesEmptyValues(t *testing.T) {
	got := (Info{}).String()
	want := "distroplane unknown\ncommit: unknown\nbuilt: unknown"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
