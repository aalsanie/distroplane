//go:build windows

package providerhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

func configureProcessTree(*exec.Cmd) {}

func terminateProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(process.Pid), "/T", "/F")
	command.WaitDelay = time.Second
	err := command.Run()
	if err == nil {
		return nil
	}
	if killErr := process.Kill(); killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	} else {
		return killErr
	}
}

const (
	processSetQuota                   = 0x0100
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x2000
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObject          = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	terminateJobObject       = kernel32.NewProc("TerminateJobObject")
)

type jobBasicLimitInformation struct {
	perProcessUserTimeLimit int64
	perJobUserTimeLimit     int64
	limitFlags              uint32
	minimumWorkingSetSize   uintptr
	maximumWorkingSetSize   uintptr
	activeProcessLimit      uint32
	affinity                uintptr
	priorityClass           uint32
	schedulingClass         uint32
}

type jobExtendedLimitInformation struct {
	basicLimitInformation jobBasicLimitInformation
	ioCounters            [6]uint64
	processMemoryLimit    uintptr
	jobMemoryLimit        uintptr
	peakProcessMemoryUsed uintptr
	peakJobMemoryUsed     uintptr
}

func attachProcessTree(process *os.Process) (func() error, func(), error) {
	fallback := func() error { return terminateProcessTree(process) }
	noop := func() {}
	handle, err := syscall.OpenProcess(processSetQuota|syscall.PROCESS_TERMINATE|syscall.SYNCHRONIZE, false, uint32(process.Pid))
	if err != nil {
		return fallback, noop, fmt.Errorf("open provider process for job assignment: %w", err)
	}
	defer syscall.CloseHandle(handle)
	raw, _, err := createJobObject.Call(0, 0)
	if raw == 0 {
		return fallback, noop, fmt.Errorf("create provider job: %w", err)
	}
	job := syscall.Handle(raw)
	release := func() { _ = syscall.CloseHandle(job) }
	limits := jobExtendedLimitInformation{}
	limits.basicLimitInformation.limitFlags = jobObjectLimitKillOnJobClose
	if ok, _, err := setInformationJobObject.Call(raw, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); ok == 0 {
		release()
		return fallback, noop, fmt.Errorf("configure provider job: %w", err)
	}
	if ok, _, err := assignProcessToJobObject.Call(raw, uintptr(handle)); ok == 0 {
		release()
		if state, waitErr := syscall.WaitForSingleObject(handle, 0); waitErr == nil && state == syscall.WAIT_OBJECT_0 {
			return noopTermination, noop, nil
		}
		return fallback, noop, fmt.Errorf("assign provider to job: %w", err)
	}
	terminate := func() error {
		if ok, _, err := terminateJobObject.Call(raw, 1); ok == 0 {
			return fmt.Errorf("terminate provider job: %w", err)
		}
		return nil
	}
	return terminate, release, nil
}

func noopTermination() error { return nil }
