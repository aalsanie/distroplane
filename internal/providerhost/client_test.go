package providerhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/protocol"
)

const helperEnabledEnv = "DISTROPLANE_PROVIDERHOST_HELPER"
const helperModeEnv = "DISTROPLANE_PROVIDERHOST_MODE"

type helperHandler struct{}

type helperConfig struct {
	Mode string `json:"mode,omitempty"`
}

func (helperHandler) Describe(context.Context, protocol.DescribeRequest) (protocol.DescribeResponse, *protocol.ProviderError) {
	provider := protocol.ProviderIdentity{Name: "fake", Version: "1.0.0"}
	versions := []string{protocol.Version}
	capabilities := []protocol.Capability{protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile}
	switch os.Getenv(helperModeEnv) {
	case "identity_other":
		provider.Name = "other"
	case "protocol_other":
		versions = []string{"2"}
	case "no_reconcile":
		capabilities = []protocol.Capability{protocol.CapabilityPlan, protocol.CapabilityApply}
	}
	return protocol.DescribeResponse{Provider: provider, ProtocolVersions: versions, Capabilities: capabilities}, nil
}

func (helperHandler) Plan(_ context.Context, request protocol.PlanRequest) (protocol.PlanResponse, *protocol.ProviderError) {
	var cfg helperConfig
	if err := json.Unmarshal(request.Target.Configuration, &cfg); err != nil {
		value := protocol.NewProviderError(protocol.ErrorConfiguration, "bad config", false)
		return protocol.PlanResponse{}, &value
	}
	if cfg.Mode == "plan_error" {
		value := protocol.NewProviderError(protocol.ErrorConfiguration, "plan failed", false)
		return protocol.PlanResponse{}, &value
	}
	payload, _ := json.Marshal(cfg)
	return protocol.PlanResponse{Operations: []protocol.PlannedOperation{{
		ID: "publish", Kind: "publish", SideEffecting: true, ProviderPayload: payload,
	}}}, nil
}

func (helperHandler) Apply(_ context.Context, request protocol.ApplyRequest) (protocol.ApplyResponse, *protocol.ProviderError) {
	var cfg helperConfig
	if err := json.Unmarshal(request.ProviderPayload, &cfg); err != nil {
		value := protocol.NewProviderError(protocol.ErrorConfiguration, "bad payload", false)
		return protocol.ApplyResponse{}, &value
	}
	switch cfg.Mode {
	case "waiting":
		return protocol.ApplyResponse{Result: protocol.DistributionResult{State: protocol.ResultWaitingExternal, ProviderState: "pending", Evidence: json.RawMessage(`{"provider":"helper"}`)}}, nil
	case "rejected":
		return protocol.ApplyResponse{Result: protocol.DistributionResult{State: protocol.ResultRejected, ProviderState: "rejected", Evidence: json.RawMessage(`{"provider":"helper"}`)}}, nil
	case "transient":
		value := protocol.NewProviderError(protocol.ErrorTransientExternal, "transient", true)
		return protocol.ApplyResponse{}, &value
	case "ambiguous":
		value := protocol.NewProviderError(protocol.ErrorAmbiguousOutcome, "ambiguous", false)
		return protocol.ApplyResponse{}, &value
	default:
		return protocol.ApplyResponse{Result: protocol.DistributionResult{State: protocol.ResultPublished, ProviderState: "published", Evidence: json.RawMessage(`{"provider":"helper"}`)}}, nil
	}
}

func (helperHandler) Reconcile(_ context.Context, request protocol.ReconcileRequest) (protocol.ReconcileResponse, *protocol.ProviderError) {
	var cfg helperConfig
	if err := json.Unmarshal(request.ProviderPayload, &cfg); err != nil {
		value := protocol.NewProviderError(protocol.ErrorConfiguration, "bad payload", false)
		return protocol.ReconcileResponse{}, &value
	}
	if cfg.Mode == "reconcile_previous" && request.Previous == nil {
		value := protocol.NewProviderError(protocol.ErrorConfiguration, "missing previous", false)
		return protocol.ReconcileResponse{}, &value
	}
	return protocol.ReconcileResponse{Result: protocol.DistributionResult{State: protocol.ResultPublished, ProviderState: "published", Evidence: json.RawMessage(`{"provider":"helper"}`)}}, nil
}

func TestProviderProcessHelper(t *testing.T) {
	if os.Getenv(helperEnabledEnv) != "1" {
		return
	}
	mode := os.Getenv(helperModeEnv)
	code := 0
	switch mode {
	case "empty":
	case "malformed":
		_, _ = os.Stdout.WriteString("{")
	case "oversized":
		_, _ = os.Stdout.WriteString(strings.Repeat("x", protocol.DefaultMaxMessageBytes+1))
	case "crash":
		_, _ = os.Stderr.WriteString("provider crash\n")
		code = 70
	case "crashlong":
		_, _ = os.Stderr.WriteString(strings.Repeat("diagnostic", 256))
		code = 70
	case "sleep":
		time.Sleep(10 * time.Second)
		code = serveHelper(os.Stdin, os.Stdout)
	case "trailing":
		var output bytes.Buffer
		code = serveHelper(os.Stdin, &output)
		if code == 0 {
			_, _ = os.Stdout.Write(output.Bytes())
			_, _ = os.Stdout.WriteString(" trailing")
		}
	case "mismatch":
		var output bytes.Buffer
		code = serveHelper(os.Stdin, &output)
		if code == 0 {
			codec := protocol.NewCodec(protocol.DefaultMaxMessageBytes)
			response, err := codec.DecodeResponse(&output)
			if err != nil {
				code = 1
			} else {
				response.RequestID = "other-" + response.RequestID
				if err := codec.EncodeResponse(os.Stdout, response); err != nil {
					code = 1
				}
			}
		}
	case "envcheck":
		if os.Getenv("SHOULD_NOT_LEAK") != "" {
			_, _ = os.Stderr.WriteString("environment leaked")
			code = 72
		} else {
			code = serveHelper(os.Stdin, os.Stdout)
		}
	default:
		code = serveHelper(os.Stdin, os.Stdout)
	}
	os.Exit(code)
}

func serveHelper(input *os.File, output interface{ Write([]byte) (int, error) }) int {
	if err := protocol.ServeOnce(context.Background(), input, output, helperHandler{}, protocol.NewCodec(protocol.DefaultMaxMessageBytes)); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func helperExecutable(t testing.TB) string {
	t.Helper()
	value, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	value, err = filepath.Abs(value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func helperClient(t testing.TB, mode string, mutate func(*Options)) *Client {
	t.Helper()
	options := Options{
		Args:        []string{"-test.run=^TestProviderProcessHelper$"},
		Environment: []string{helperEnabledEnv + "=1", helperModeEnv + "=" + mode},
		WaitDelay:   time.Second,
	}
	if mutate != nil {
		mutate(&options)
	}
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func helperEndpoint(t testing.TB) planner.Endpoint {
	return planner.Endpoint{Name: "fake", Executable: helperExecutable(t)}
}

func planRequest(mode string) protocol.PlanRequest {
	return protocol.PlanRequest{
		Release: protocol.Release{ID: "r", Artifacts: []protocol.Artifact{{Name: "a", Digest: "sha256:" + strings.Repeat("a", 64), Size: 1}}},
		Target:  protocol.Target{ID: "t", Configuration: json.RawMessage(`{"mode":"` + mode + `"}`)},
	}
}

func TestClientDescribeAndPlan(t *testing.T) {
	client := helperClient(t, "normal", nil)
	description, err := client.Describe(context.Background(), helperEndpoint(t))
	if err != nil {
		t.Fatal(err)
	}
	if description.Provider.Name != "fake" || description.Provider.Version != "1.0.0" {
		t.Fatalf("description=%+v", description)
	}
	plan, err := client.Plan(context.Background(), helperEndpoint(t), planRequest(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].ID != "publish" {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestClientRejectsProcessAndProtocolFailures(t *testing.T) {
	for _, mode := range []string{"empty", "malformed", "oversized", "crash", "trailing", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			client := helperClient(t, mode, nil)
			_, err := client.Describe(context.Background(), helperEndpoint(t))
			if err == nil {
				t.Fatal("failure accepted")
			}
			if mode == "oversized" && !errors.Is(err, ErrOutputLimit) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestClientCancellationKillsProcess(t *testing.T) {
	client := helperClient(t, "sleep", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Describe(ctx, helperEndpoint(t))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}

func TestClientBoundsDiagnostics(t *testing.T) {
	client := helperClient(t, "crashlong", func(options *Options) { options.MaxStderrBytes = 32 })
	_, err := client.Describe(context.Background(), helperEndpoint(t))
	var processErr *ProcessError
	if !errors.As(err, &processErr) {
		t.Fatalf("err=%v", err)
	}
	if !processErr.Truncated || len(processErr.Diagnostics) > 32 {
		t.Fatalf("processErr=%+v", processErr)
	}
}

func TestClientDoesNotInheritEnvironment(t *testing.T) {
	t.Setenv("SHOULD_NOT_LEAK", "secret")
	client := helperClient(t, "envcheck", nil)
	if _, err := client.Describe(context.Background(), helperEndpoint(t)); err != nil {
		t.Fatal(err)
	}
}

func TestClientProviderErrorAndConcurrency(t *testing.T) {
	client := helperClient(t, "normal", nil)
	_, err := client.Plan(context.Background(), helperEndpoint(t), planRequest("plan_error"))
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Value.Code != protocol.ErrorConfiguration {
		t.Fatalf("err=%v", err)
	}

	const count = 12
	var wg sync.WaitGroup
	errorsCh := make(chan error, count)
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Describe(context.Background(), helperEndpoint(t))
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestClientValidation(t *testing.T) {
	for _, options := range []Options{{MaxMessageBytes: -1}, {MaxStderrBytes: -1}, {WaitDelay: -1}, {Environment: []string{"bad"}}, {Environment: []string{"=bad"}}} {
		if _, err := New(options); err == nil {
			t.Fatalf("options=%+v accepted", options)
		}
	}
	client := helperClient(t, "normal", nil)
	if _, err := client.Describe(nil, helperEndpoint(t)); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := client.Describe(context.Background(), planner.Endpoint{Executable: "relative"}); err == nil {
		t.Fatal("relative executable accepted")
	}
	if _, err := client.Describe(context.Background(), planner.Endpoint{Executable: t.TempDir()}); err == nil {
		t.Fatal("directory executable accepted")
	}
	if _, err := client.Describe(context.Background(), planner.Endpoint{Executable: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing executable accepted")
	}
	var nilClient *Client
	if _, err := nilClient.Describe(context.Background(), helperEndpoint(t)); err == nil {
		t.Fatal("nil client accepted")
	}
	if err := client.call(context.Background(), helperExecutable(t), "bad", struct{}{}, &struct{}{}); err == nil {
		t.Fatal("invalid operation accepted")
	}
	if err := client.call(context.Background(), helperExecutable(t), protocol.OperationDescribe, protocol.DescribeRequest{}, nil); err == nil {
		t.Fatal("nil destination accepted")
	}
}

func TestWriters(t *testing.T) {
	limited := &limitedWriter{limit: 3}
	if n, err := limited.Write([]byte("ab")); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := limited.Write([]byte("cd")); !errors.Is(err, ErrOutputLimit) || !limited.exceeded || string(limited.Bytes()) != "abc" {
		t.Fatalf("limited=%+v err=%v", limited, err)
	}
	if _, err := limited.Write([]byte("x")); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("err=%v", err)
	}
	capture := &captureWriter{limit: 3}
	if n, err := capture.Write([]byte("abcd")); err != nil || n != 4 || !capture.truncated || capture.String() != "abc" {
		t.Fatalf("capture=%+v n=%d err=%v", capture, n, err)
	}
	capture = &captureWriter{limit: 8}
	_, _ = capture.Write([]byte{'a', 0xff, 'b'})
	if !strings.Contains(capture.String(), "�") {
		t.Fatalf("capture=%q", capture.String())
	}
}

func TestProcessAndProviderErrorMethods(t *testing.T) {
	base := errors.New("boom")
	processErr := &ProcessError{Executable: "/x", Err: base, Diagnostics: "diag", Truncated: true}
	if !errors.Is(processErr, base) || !strings.Contains(processErr.Error(), "stderr truncated") {
		t.Fatalf("err=%v", processErr)
	}
	var nilProcess *ProcessError
	if nilProcess.Error() != "" || nilProcess.Unwrap() != nil {
		t.Fatal("nil process error mismatch")
	}
	providerErr := &ProviderError{Value: protocol.NewProviderError(protocol.ErrorRejected, "no", false)}
	if providerErr.Error() != "REJECTED: no" {
		t.Fatalf("err=%q", providerErr.Error())
	}
	var nilProvider *ProviderError
	if nilProvider.Error() != "" {
		t.Fatal("nil provider error mismatch")
	}
}
