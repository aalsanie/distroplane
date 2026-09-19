//go:build !linux && !darwin && !windows

package journal

import (
	"errors"
	"fmt"
	"os"
	"runtime"
)

var errJournalFileLocked = errors.New("journal file lock held")

func lockJournalFile(*os.File) error {
	return fmt.Errorf("journal file locking is unsupported on %s", runtime.GOOS)
}

func unlockJournalFile(*os.File) error {
	return nil
}
