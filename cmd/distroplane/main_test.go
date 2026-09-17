package main

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestMainDelegatesToCLI(t *testing.T) {
	oldArguments, oldStdout, oldStderr, oldExit := arguments, stdout, stderr, exit
	t.Cleanup(func() {
		arguments, stdout, stderr, exit = oldArguments, oldStdout, oldStderr, oldExit
	})

	var out bytes.Buffer
	arguments = func() []string { return []string{"version"} }
	stdout = &out
	stderr = io.Discard
	gotExit := -1
	exit = func(code int) { gotExit = code }

	main()

	if gotExit != 0 {
		t.Fatalf("exit code = %d, want 0", gotExit)
	}
	if out.Len() == 0 {
		t.Fatal("expected version output")
	}
}

func TestArgumentsUsesProcessArguments(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"distroplane", "version"}

	got := arguments()
	if len(got) != 1 || got[0] != "version" {
		t.Fatalf("arguments() = %#v, want [version]", got)
	}
}
