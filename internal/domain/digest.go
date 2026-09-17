package domain

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type Digest struct {
	bytes [32]byte
	set   bool
}

func NewSHA256Digest(value string) (Digest, error) {
	if len(value) != 64 {
		return Digest{}, fmt.Errorf("sha256 digest must contain 64 hexadecimal characters")
	}
	if value != strings.ToLower(value) {
		return Digest{}, fmt.Errorf("sha256 digest must use lowercase hexadecimal")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return Digest{}, fmt.Errorf("invalid sha256 digest: %w", err)
	}
	var digest Digest
	copy(digest.bytes[:], decoded)
	digest.set = true
	return digest, nil
}

func ParseDigest(value string) (Digest, error) {
	algorithm, encoded, ok := strings.Cut(value, ":")
	if !ok || algorithm != "sha256" {
		return Digest{}, fmt.Errorf("digest must use sha256:<hex> format")
	}
	return NewSHA256Digest(encoded)
}

func (d Digest) Algorithm() string { return "sha256" }
func (d Digest) Hex() string       { return hex.EncodeToString(d.bytes[:]) }
func (d Digest) String() string    { return d.Algorithm() + ":" + d.Hex() }
func (d Digest) Valid() bool       { return d.set }

func (d Digest) MarshalJSON() ([]byte, error) {
	if !d.Valid() {
		return nil, fmt.Errorf("invalid zero digest")
	}
	return json.Marshal(d.String())
}

func (d *Digest) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseDigest(value)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
