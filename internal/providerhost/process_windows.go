//go:build windows

package providerhost

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

func configureProcessTree(*exec.Cmd) {}

func terminateProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	err := exec.Command("taskkill", "/PID", strconv.Itoa(process.Pid), "/T", "/F").Run()
	if err == nil {
		return nil
	}
	if killErr := process.Kill(); killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	} else {
		return killErr
	}
}
