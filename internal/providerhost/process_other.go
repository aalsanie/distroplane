//go:build !linux && !darwin && !windows

package providerhost

import (
	"errors"
	"os"
	"os/exec"
)

func configureProcessTree(*exec.Cmd) {}

func terminateProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := process.Kill(); err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	} else {
		return err
	}
}
