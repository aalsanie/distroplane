package protocol

import (
	"encoding/json"
	"fmt"
)

type ErrorCode string

const (
	ErrorProtocol          ErrorCode = "PROTOCOL_ERROR"
	ErrorConfiguration     ErrorCode = "CONFIGURATION_ERROR"
	ErrorAuthentication    ErrorCode = "AUTHENTICATION_ERROR"
	ErrorAuthorization     ErrorCode = "AUTHORIZATION_ERROR"
	ErrorTransientExternal ErrorCode = "TRANSIENT_EXTERNAL_ERROR"
	ErrorPermanentExternal ErrorCode = "PERMANENT_EXTERNAL_ERROR"
	ErrorRejected          ErrorCode = "REJECTED"
	ErrorTimeout           ErrorCode = "TIMEOUT"
	ErrorCancelled         ErrorCode = "CANCELLED"
	ErrorProviderInternal  ErrorCode = "PROVIDER_INTERNAL_ERROR"
	ErrorAmbiguousOutcome  ErrorCode = "AMBIGUOUS_OUTCOME"
)

func (c ErrorCode) Valid() bool {
	switch c {
	case ErrorProtocol, ErrorConfiguration, ErrorAuthentication, ErrorAuthorization,
		ErrorTransientExternal, ErrorPermanentExternal, ErrorRejected, ErrorTimeout,
		ErrorCancelled, ErrorProviderInternal, ErrorAmbiguousOutcome:
		return true
	default:
		return false
	}
}

type ProviderError struct {
	Code      ErrorCode       `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	Details   json.RawMessage `json:"details,omitempty"`
}

func (e ProviderError) Validate() error {
	if !e.Code.Valid() {
		return fmt.Errorf("invalid error code %q", e.Code)
	}
	if err := validateText("error message", e.Message, 4096, false); err != nil {
		return err
	}
	if len(e.Details) != 0 && !json.Valid(e.Details) {
		return fmt.Errorf("error details must be valid JSON")
	}
	return nil
}

func NewProviderError(code ErrorCode, message string, retryable bool) ProviderError {
	return ProviderError{Code: code, Message: message, Retryable: retryable}
}
