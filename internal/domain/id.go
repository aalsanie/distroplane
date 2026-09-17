package domain

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxIdentifierLength = 256

type ReleaseID string
type TargetID string
type OperationID string
type PlanID string
type RunID string
type ProviderName string
type ProviderVersion string
type CredentialRef string

type AttemptNumber uint32

func NewReleaseID(value string) (ReleaseID, error) {
	if err := validateIdentifier("release ID", value); err != nil {
		return "", err
	}
	return ReleaseID(value), nil
}

func NewTargetID(value string) (TargetID, error) {
	if err := validateIdentifier("target ID", value); err != nil {
		return "", err
	}
	return TargetID(value), nil
}

func NewOperationID(value string) (OperationID, error) {
	if err := validateIdentifier("operation ID", value); err != nil {
		return "", err
	}
	return OperationID(value), nil
}

func NewPlanID(value string) (PlanID, error) {
	if err := validateIdentifier("plan ID", value); err != nil {
		return "", err
	}
	return PlanID(value), nil
}

func NewRunID(value string) (RunID, error) {
	if err := validateIdentifier("run ID", value); err != nil {
		return "", err
	}
	return RunID(value), nil
}

func NewProviderName(value string) (ProviderName, error) {
	if err := validateIdentifier("provider name", value); err != nil {
		return "", err
	}
	return ProviderName(value), nil
}

func NewProviderVersion(value string) (ProviderVersion, error) {
	if err := validateIdentifier("provider version", value); err != nil {
		return "", err
	}
	return ProviderVersion(value), nil
}

func NewCredentialRef(value string) (CredentialRef, error) {
	if err := validateIdentifier("credential reference", value); err != nil {
		return "", err
	}
	return CredentialRef(value), nil
}

func NewAttemptNumber(value uint32) (AttemptNumber, error) {
	if value == 0 {
		return 0, fmt.Errorf("attempt number must be greater than zero")
	}
	return AttemptNumber(value), nil
}

func (v ReleaseID) Valid() bool    { return validateIdentifier("release ID", string(v)) == nil }
func (v TargetID) Valid() bool     { return validateIdentifier("target ID", string(v)) == nil }
func (v OperationID) Valid() bool  { return validateIdentifier("operation ID", string(v)) == nil }
func (v PlanID) Valid() bool       { return validateIdentifier("plan ID", string(v)) == nil }
func (v RunID) Valid() bool        { return validateIdentifier("run ID", string(v)) == nil }
func (v ProviderName) Valid() bool { return validateIdentifier("provider name", string(v)) == nil }
func (v ProviderVersion) Valid() bool {
	return validateIdentifier("provider version", string(v)) == nil
}
func (v CredentialRef) Valid() bool {
	return validateIdentifier("credential reference", string(v)) == nil
}
func (v AttemptNumber) Valid() bool { return v > 0 }

func validateIdentifier(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if len(value) > maxIdentifierLength {
		return fmt.Errorf("%s exceeds %d bytes", name, maxIdentifierLength)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have surrounding whitespace", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}
