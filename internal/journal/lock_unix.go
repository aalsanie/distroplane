//go:build linux || darwin

package journal

import (
	"errors"
	"os"
	"syscall"
)

var errJournalFileLocked = errors.New("journal file lock held")

func lockJournalFile(file *os.File) error {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errJournalFileLocked
		}
		return err
	}
	return nil
}

func unlockJournalFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
