package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ReviewField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type ReviewFields []ReviewField

func (fields ReviewFields) Validate() error {
	if len(fields) > 16 {
		return fmt.Errorf("reviewFields exceeds 16 entries")
	}
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		if !validReviewText(field.Label, 80) || !validReviewText(field.Value, 512) {
			return fmt.Errorf("reviewFields contains invalid label or value")
		}
		if seen[field.Label] {
			return fmt.Errorf("reviewFields contains duplicate label")
		}
		seen[field.Label] = true
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if len(raw) > 8192 {
		return fmt.Errorf("reviewFields exceeds 8192 canonical JSON bytes")
	}
	return nil
}

func validReviewText(value string, limit int) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || value == "" || utf8.RuneCountInString(value) > limit {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) || r == utf8.RuneError {
			return false
		}
	}
	return true
}

func (fields *ReviewFields) UnmarshalJSON(raw []byte) error {
	if !utf8.Valid(raw) || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		return fmt.Errorf("reviewFields must be an array")
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return err
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("reviewFields must contain label/value objects")
	}
	if len(entries) > 16 {
		return fmt.Errorf("reviewFields exceeds 16 entries")
	}
	result := make(ReviewFields, len(entries))
	for i, entry := range entries {
		if len(entry) != 2 || json.Unmarshal(entry["label"], &result[i].Label) != nil || json.Unmarshal(entry["value"], &result[i].Value) != nil {
			return fmt.Errorf("reviewFields entries require exactly label and value")
		}
	}
	if err := result.Validate(); err != nil {
		return err
	}
	*fields = result
	return nil
}
