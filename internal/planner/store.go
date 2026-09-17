package planner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Store struct {
	Root string
}

func (s Store) Save(plan DistributionPlan) (string, error) {
	if strings.TrimSpace(s.Root) == "" {
		return "", fmt.Errorf("store root must not be empty")
	}
	if !plan.ID().Valid() {
		return "", fmt.Errorf("plan ID is invalid")
	}
	data, err := plan.Bytes()
	if err != nil {
		return "", err
	}

	directory := filepath.Join(s.Root, "plans")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	final := filepath.Join(directory, string(plan.ID())+".json")
	if same, err := existingMatches(final, data); err != nil {
		return "", err
	} else if same {
		return final, nil
	}

	temp, err := os.CreateTemp(directory, ".plan-*")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tempName, 0o644); err != nil {
		return "", err
	}

	if err := os.Link(tempName, final); err != nil {
		if same, existingErr := existingMatches(final, data); existingErr == nil && same {
			return final, nil
		}
		if _, statErr := os.Stat(final); statErr == nil {
			return "", fmt.Errorf("plan %q already exists with different content", plan.ID())
		}
		return "", fmt.Errorf("persist plan %q: %w", plan.ID(), err)
	}
	if err := syncDirectory(directory); err != nil {
		return "", err
	}
	return final, nil
}

func existingMatches(path string, expected []byte) (bool, error) {
	actual, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !bytes.Equal(actual, expected) {
		return false, fmt.Errorf("plan file %q exists with different content", path)
	}
	return true, nil
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
