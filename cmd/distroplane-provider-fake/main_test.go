package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/protocol"
)

func describeInput(t *testing.T) string {
	t.Helper()
	request := protocol.Request{ProtocolVersion: protocol.Version, RequestID: "r", Operation: protocol.OperationDescribe, Payload: json.RawMessage(`{}`)}
	var buffer bytes.Buffer
	if err := protocol.NewCodec(protocol.DefaultMaxMessageBytes).EncodeRequest(&buffer, request); err != nil {
		t.Fatal(err)
	}
	return buffer.String()
}

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestRunNormal(t *testing.T) {
	var out, diagnostics bytes.Buffer
	code := run(context.Background(), strings.NewReader(describeInput(t)), &out, &diagnostics, env(nil), func(time.Duration) {})
	if code != 0 {
		t.Fatalf("code=%d diagnostics=%s", code, diagnostics.String())
	}
	response, err := protocol.NewCodec(protocol.DefaultMaxMessageBytes).DecodeResponse(&out)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != protocol.StatusOK {
		t.Fatal("unexpected status")
	}
}

func TestRunProcessModes(t *testing.T) {
	input := describeInput(t)
	cases := []struct {
		mode  string
		code  int
		check func(*testing.T, string, string)
	}{
		{"empty", 0, func(t *testing.T, out, _ string) {
			if out != "" {
				t.Fatal("expected empty output")
			}
		}},
		{"malformed", 0, func(t *testing.T, out, _ string) {
			if out != "{" {
				t.Fatal("expected malformed output")
			}
		}},
		{"oversized", 0, func(t *testing.T, out, _ string) {
			if len(out) != protocol.DefaultMaxMessageBytes+1 {
				t.Fatal("wrong oversized length")
			}
		}},
		{"crash", 70, func(t *testing.T, _, diagnostics string) {
			if !strings.Contains(diagnostics, "crash") {
				t.Fatal("missing crash diagnostics")
			}
		}},
		{"trailing", 0, func(t *testing.T, out, _ string) {
			if !strings.HasSuffix(out, " trailing") {
				t.Fatal("missing trailing garbage")
			}
		}},
		{"mismatched_request_id", 0, func(t *testing.T, out, _ string) {
			response, err := protocol.NewCodec(protocol.DefaultMaxMessageBytes).DecodeResponse(strings.NewReader(out))
			if err != nil {
				t.Fatal(err)
			}
			if response.RequestID != "mismatched-r" {
				t.Fatalf("unexpected request ID %q", response.RequestID)
			}
		}},
		{"unknown", 64, func(t *testing.T, _, diagnostics string) {
			if !strings.Contains(diagnostics, "unknown") {
				t.Fatal("missing unknown diagnostics")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			code := run(context.Background(), strings.NewReader(input), &out, &diagnostics, env(map[string]string{processModeEnv: tc.mode}), func(time.Duration) {})
			if code != tc.code {
				t.Fatalf("code=%d want=%d", code, tc.code)
			}
			tc.check(t, out.String(), diagnostics.String())
		})
	}
}

func TestRunTimeout(t *testing.T) {
	var out, diagnostics bytes.Buffer
	var slept time.Duration
	code := run(context.Background(), strings.NewReader(describeInput(t)), &out, &diagnostics, env(map[string]string{processModeEnv: "timeout", delayMillisEnv: "25"}), func(value time.Duration) { slept = value })
	if code != 0 || slept != 25*time.Millisecond {
		t.Fatalf("code=%d slept=%s", code, slept)
	}

	out.Reset()
	diagnostics.Reset()
	code = run(context.Background(), strings.NewReader(describeInput(t)), &out, &diagnostics, env(map[string]string{processModeEnv: "timeout", delayMillisEnv: "bad"}), func(time.Duration) {})
	if code != 64 || !strings.Contains(diagnostics.String(), "invalid fake delay") {
		t.Fatal("invalid delay not rejected")
	}

	out.Reset()
	diagnostics.Reset()
	slept = 0
	code = run(context.Background(), strings.NewReader(describeInput(t)), &out, &diagnostics, env(map[string]string{processModeEnv: "timeout"}), func(value time.Duration) { slept = value })
	if code != 0 || slept != 10*time.Second {
		t.Fatalf("default timeout mismatch: %d %s", code, slept)
	}
}

func TestRunProtocolFailure(t *testing.T) {
	var out, diagnostics bytes.Buffer
	code := run(context.Background(), strings.NewReader("bad"), &out, &diagnostics, env(nil), func(time.Duration) {})
	if code != 1 || diagnostics.Len() == 0 {
		t.Fatal("protocol failure not surfaced")
	}

	out.Reset()
	diagnostics.Reset()
	code = run(context.Background(), strings.NewReader("bad"), &out, &diagnostics, env(map[string]string{processModeEnv: "trailing"}), func(time.Duration) {})
	if code != 1 || diagnostics.Len() == 0 {
		t.Fatal("trailing preflight failure not surfaced")
	}
}

func TestRunProviderErrorUsesProtocolResponse(t *testing.T) {
	payload, err := json.Marshal(protocol.ApplyRequest{PlanID: "p", TargetID: "t", OperationID: "o", IdempotencyKey: "k", Attempt: 1, ProviderPayload: json.RawMessage(`{"mode":"ambiguous"}`)})
	if err != nil {
		t.Fatal(err)
	}
	request := protocol.Request{ProtocolVersion: protocol.Version, RequestID: "r", Operation: protocol.OperationApply, Payload: payload}
	var input bytes.Buffer
	codec := protocol.NewCodec(protocol.DefaultMaxMessageBytes)
	if err := codec.EncodeRequest(&input, request); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	code := run(context.Background(), &input, &output, &diagnostics, env(nil), func(time.Duration) {})
	if code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("code=%d diagnostics=%s", code, diagnostics.String())
	}
	response, err := codec.DecodeResponse(&output)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != protocol.StatusError || response.Error == nil || response.Error.Code != protocol.ErrorAmbiguousOutcome {
		t.Fatalf("unexpected response: %+v", response)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failure") }

func TestRunMismatchedRequestIDFailures(t *testing.T) {
	var out, diagnostics bytes.Buffer
	code := run(context.Background(), strings.NewReader("bad"), &out, &diagnostics, env(map[string]string{processModeEnv: "mismatched_request_id"}), func(time.Duration) {})
	if code != 1 || diagnostics.Len() == 0 {
		t.Fatal("mismatched request ID malformed input failure not surfaced")
	}

	diagnostics.Reset()
	code = run(context.Background(), strings.NewReader(describeInput(t)), failingWriter{}, &diagnostics, env(map[string]string{processModeEnv: "mismatched_request_id"}), func(time.Duration) {})
	if code != 1 || diagnostics.Len() == 0 {
		t.Fatal("mismatched request ID output failure not surfaced")
	}
}
