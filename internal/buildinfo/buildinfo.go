// Package buildinfo exposes immutable build metadata injected at link time.
package buildinfo

import "fmt"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info is the user-visible identity of a Distroplane binary.
type Info struct {
	Version   string
	Commit    string
	BuildDate string
}

// Current returns the metadata compiled into the running binary.
func Current() Info {
	return Info{
		Version:   normalized(Version),
		Commit:    normalized(Commit),
		BuildDate: normalized(BuildDate),
	}
}

// String renders stable human-readable version information.
func (i Info) String() string {
	return fmt.Sprintf("distroplane %s\ncommit: %s\nbuilt: %s", normalized(i.Version), normalized(i.Commit), normalized(i.BuildDate))
}

func normalized(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}
