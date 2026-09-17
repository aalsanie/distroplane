package planner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/aalsanie/distroplane/internal/domain"
)

type ArtifactHasher interface {
	Hash(path string) (domain.Digest, int64, error)
}

type FileHasher struct {
	afterRead func()
}

func (h FileHasher) Hash(path string) (domain.Digest, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return domain.Digest{}, 0, err
	}
	defer file.Close()

	before, err := file.Stat()
	if err != nil {
		return domain.Digest{}, 0, err
	}
	if !before.Mode().IsRegular() {
		return domain.Digest{}, 0, fmt.Errorf("artifact %q is not a regular file", path)
	}

	hasher := sha256.New()
	written, err := io.Copy(hasher, file)
	if err != nil {
		return domain.Digest{}, 0, err
	}
	if h.afterRead != nil {
		h.afterRead()
	}
	after, err := file.Stat()
	if err != nil {
		return domain.Digest{}, 0, err
	}
	if written != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return domain.Digest{}, 0, fmt.Errorf("artifact %q changed while hashing", path)
	}

	digest, err := domain.NewSHA256Digest(hex.EncodeToString(hasher.Sum(nil)))
	if err != nil {
		return domain.Digest{}, 0, err
	}
	return digest, written, nil
}
