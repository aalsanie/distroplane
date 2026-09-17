package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Version                = "1"
	DefaultMaxMessageBytes = 1 << 20
)

type Operation string

const (
	OperationDescribe  Operation = "describe"
	OperationPlan      Operation = "plan"
	OperationApply     Operation = "apply"
	OperationReconcile Operation = "reconcile"
)

func (o Operation) Valid() bool {
	switch o {
	case OperationDescribe, OperationPlan, OperationApply, OperationReconcile:
		return true
	default:
		return false
	}
}

type Status string

const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

func (s Status) Valid() bool { return s == StatusOK || s == StatusError }

type Request struct {
	ProtocolVersion string          `json:"protocolVersion"`
	RequestID       string          `json:"requestId"`
	Operation       Operation       `json:"operation"`
	Payload         json.RawMessage `json:"payload"`
}

func (r Request) Validate() error {
	if err := validateVersion(r.ProtocolVersion); err != nil {
		return err
	}
	if err := validateText("request ID", r.RequestID, 128, false); err != nil {
		return err
	}
	if err := validateText("operation", string(r.Operation), 32, false); err != nil {
		return err
	}
	if len(r.Payload) == 0 || !json.Valid(r.Payload) {
		return fmt.Errorf("request payload must be valid JSON")
	}
	return nil
}

func (r Request) CheckVersion() error {
	if r.ProtocolVersion != Version {
		return fmt.Errorf("unsupported protocol version %q", r.ProtocolVersion)
	}
	return nil
}

type Response struct {
	ProtocolVersion string          `json:"protocolVersion"`
	RequestID       string          `json:"requestId"`
	Operation       Operation       `json:"operation"`
	Status          Status          `json:"status"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	Error           *ProviderError  `json:"error,omitempty"`
}

func (r Response) Validate() error {
	if err := validateVersion(r.ProtocolVersion); err != nil {
		return err
	}
	if err := validateText("request ID", r.RequestID, 128, false); err != nil {
		return err
	}
	if err := validateText("operation", string(r.Operation), 32, false); err != nil {
		return err
	}
	if !r.Status.Valid() {
		return fmt.Errorf("invalid response status %q", r.Status)
	}
	switch r.Status {
	case StatusOK:
		if r.Error != nil {
			return fmt.Errorf("successful response must not contain an error")
		}
		if len(r.Payload) == 0 || !json.Valid(r.Payload) {
			return fmt.Errorf("successful response payload must be valid JSON")
		}
	case StatusError:
		if len(r.Payload) != 0 {
			return fmt.Errorf("error response must not contain a payload")
		}
		if r.Error == nil {
			return fmt.Errorf("error response must contain an error")
		}
		if err := r.Error.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (r Response) CheckCorrelation(request Request) error {
	if r.ProtocolVersion != request.ProtocolVersion {
		return fmt.Errorf("response protocol version %q does not match request %q", r.ProtocolVersion, request.ProtocolVersion)
	}
	if r.RequestID != request.RequestID {
		return fmt.Errorf("response request ID %q does not match request %q", r.RequestID, request.RequestID)
	}
	if r.Operation != request.Operation {
		return fmt.Errorf("response operation %q does not match request %q", r.Operation, request.Operation)
	}
	return nil
}

func validateVersion(value string) error {
	if err := validateText("protocol version", value, 16, false); err != nil {
		return err
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return fmt.Errorf("protocol version must be numeric")
		}
	}
	return nil
}

func validateText(name, value string, max int, optional bool) error {
	if value == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("%s must not be empty", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if len(value) > max {
		return fmt.Errorf("%s exceeds %d bytes", name, max)
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
