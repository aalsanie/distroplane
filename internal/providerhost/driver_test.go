package providerhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/executor"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func testProviderRef(t testing.TB) domain.ProviderRef {
	t.Helper()
	name, err := domain.NewProviderName("fake")
	if err != nil {
		t.Fatal(err)
	}
	version, err := domain.NewProviderVersion("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := domain.NewProviderRef(name, version)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func testExecutorRequest(t testing.TB, mode string, sideEffecting bool, previous *executor.Previous) executor.Request {
	t.Helper()
	provider := testProviderRef(t)
	operationID, _ := domain.NewOperationID("op-a")
	targetID, _ := domain.NewTargetID("target-a")
	payload, err := domain.NewJSONValue([]byte(`{"mode":"` + mode + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	key := ""
	if sideEffecting {
		key = "idempotency-key"
	}
	operation, err := domain.NewOperation(operationID, targetID, provider, "publish", nil, sideEffecting, key, time.Second, payload)
	if err != nil {
		t.Fatal(err)
	}
	planID, _ := domain.NewPlanID("plan-1")
	run, _ := domain.NewRunID("run-1")
	return executor.Request{PlanID: planID, RunID: run, Operation: operation, Attempt: 1, Previous: previous}
}

func helperDriver(t testing.TB, mode string) *Driver {
	t.Helper()
	client := helperClient(t, mode, nil)
	driver, err := NewDriver(client, []Binding{{Provider: testProviderRef(t), Executable: helperExecutable(t)}})
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func TestDriverApplyMapsResults(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want domain.NormalizedState
	}{
		{"", domain.StatePublished},
		{"waiting", domain.StateWaitingExternal},
		{"rejected", domain.StateRejected},
	} {
		t.Run(tc.mode+string(tc.want), func(t *testing.T) {
			result, err := helperDriver(t, "normal").Apply(context.Background(), testExecutorRequest(t, tc.mode, true, nil))
			if err != nil {
				t.Fatal(err)
			}
			if result.State != tc.want || len(result.Evidence) == 0 {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestDriverMapsStructuredErrors(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		code      string
		retryable bool
		ambiguous bool
	}{
		{"transient", string(protocol.ErrorTransientExternal), true, false},
		{"ambiguous", string(protocol.ErrorAmbiguousOutcome), false, true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			_, err := helperDriver(t, "normal").Apply(context.Background(), testExecutorRequest(t, tc.mode, true, nil))
			var driverErr *executor.DriverError
			if !errors.As(err, &driverErr) {
				t.Fatalf("err=%v", err)
			}
			if driverErr.Code != tc.code || driverErr.Retryable != tc.retryable || driverErr.Ambiguous != tc.ambiguous {
				t.Fatalf("driverErr=%+v", driverErr)
			}
		})
	}
}

func TestDriverProcessFailureStaysInfrastructureError(t *testing.T) {
	_, err := helperDriver(t, "crash").Apply(context.Background(), testExecutorRequest(t, "", true, nil))
	var driverErr *executor.DriverError
	if errors.As(err, &driverErr) {
		t.Fatalf("process failure mapped to driver error: %+v", driverErr)
	}
	var processErr *ProcessError
	if !errors.As(err, &processErr) {
		t.Fatalf("err=%v", err)
	}
}

func TestDriverReconcilePreviousMapping(t *testing.T) {
	previous := &executor.Previous{
		State:         domain.StateWaitingExternal,
		ProviderState: "pending",
		Evidence:      json.RawMessage(`{"provider":"helper"}`),
		ErrorCode:     "IGNORED_BY_PROTOCOL_V1",
		Ambiguous:     true,
	}
	result, err := helperDriver(t, "normal").Reconcile(context.Background(), testExecutorRequest(t, "reconcile_previous", true, previous))
	if err != nil {
		t.Fatal(err)
	}
	if result.State != domain.StatePublished {
		t.Fatalf("result=%+v", result)
	}

	running := *previous
	running.State = domain.StateRunning
	_, err = helperDriver(t, "normal").Reconcile(context.Background(), testExecutorRequest(t, "reconcile_previous", true, &running))
	var driverErr *executor.DriverError
	if !errors.As(err, &driverErr) || driverErr.Code != string(protocol.ErrorConfiguration) {
		t.Fatalf("err=%v", err)
	}
}

func TestDriverVerifiesProviderBinding(t *testing.T) {
	request := testExecutorRequest(t, "", true, nil)
	for _, tc := range []struct {
		mode string
		code string
	}{
		{"identity_other", "PROVIDER_IDENTITY_MISMATCH"},
		{"protocol_other", "PROVIDER_PROTOCOL_MISMATCH"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			_, err := helperDriver(t, tc.mode).Apply(context.Background(), request)
			var driverErr *executor.DriverError
			if !errors.As(err, &driverErr) || driverErr.Code != tc.code || driverErr.Ambiguous || driverErr.Retryable {
				t.Fatalf("err=%v", err)
			}
		})
	}
	_, err := helperDriver(t, "no_reconcile").Reconcile(context.Background(), request)
	var driverErr *executor.DriverError
	if !errors.As(err, &driverErr) || driverErr.Code != "PROVIDER_CAPABILITY_MISMATCH" {
		t.Fatalf("err=%v", err)
	}
}

func TestDriverNonSideEffectingOperationGetsStableExecutionKey(t *testing.T) {
	request := testExecutorRequest(t, "", false, nil)
	first := executionKey(request)
	second := executionKey(request)
	if first == "" || first != second || !strings.HasPrefix(first, "op-") {
		t.Fatalf("keys=%q %q", first, second)
	}
	if request.Operation.IdempotencyKey() != "" {
		t.Fatal("domain operation unexpectedly has an idempotency key")
	}
	result, err := helperDriver(t, "normal").Apply(context.Background(), request)
	if err != nil || result.State != domain.StatePublished {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDriverPreservesPlannedIdempotencyKey(t *testing.T) {
	request := testExecutorRequest(t, "", true, nil)
	if got := executionKey(request); got != request.Operation.IdempotencyKey() {
		t.Fatalf("key=%q", got)
	}
}

func TestDriverValidationAndBindings(t *testing.T) {
	client := helperClient(t, "normal", nil)
	if _, err := NewDriver(nil, nil); err == nil {
		t.Fatal("nil client accepted")
	}
	if _, err := NewDriver(client, []Binding{{}}); err == nil {
		t.Fatal("invalid provider accepted")
	}
	ref := testProviderRef(t)
	if _, err := NewDriver(client, []Binding{{Provider: ref, Executable: "relative"}}); err == nil {
		t.Fatal("relative executable accepted")
	}
	if _, err := NewDriver(client, []Binding{{Provider: ref, Executable: helperExecutable(t)}, {Provider: ref, Executable: helperExecutable(t)}}); err == nil {
		t.Fatal("duplicate binding accepted")
	}
	driver, err := NewDriver(client, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := testExecutorRequest(t, "", true, nil)
	if _, err := driver.Apply(context.Background(), request); err == nil {
		t.Fatal("unbound provider accepted")
	}
	bound := helperDriver(t, "normal")
	if _, err := bound.Apply(nil, request); err == nil {
		t.Fatal("nil context accepted")
	}
	bad := request
	bad.Attempt = 0
	if _, err := bound.Apply(context.Background(), bad); err == nil {
		t.Fatal("zero attempt accepted")
	}
	bad = request
	bad.PlanID = ""
	if _, err := bound.Apply(context.Background(), bad); err == nil {
		t.Fatal("invalid plan accepted")
	}
	bad = request
	bad.RunID = ""
	if _, err := bound.Apply(context.Background(), bad); err == nil {
		t.Fatal("invalid run accepted")
	}
	var nilDriver *Driver
	if _, err := nilDriver.Apply(context.Background(), request); err == nil {
		t.Fatal("nil driver accepted")
	}
}

func TestStateAndPreviousMapping(t *testing.T) {
	for _, tc := range []struct {
		domainState domain.NormalizedState
		protocol    protocol.ResultState
	}{
		{domain.StateWaitingExternal, protocol.ResultWaitingExternal},
		{domain.StatePublished, protocol.ResultPublished},
		{domain.StateRejected, protocol.ResultRejected},
	} {
		got, ok := toProtocolState(tc.domainState)
		if !ok || got != tc.protocol {
			t.Fatalf("to protocol: %s -> %s %v", tc.domainState, got, ok)
		}
		back, ok := fromProtocolState(tc.protocol)
		if !ok || back != tc.domainState {
			t.Fatalf("from protocol: %s -> %s %v", tc.protocol, back, ok)
		}
	}
	if _, ok := toProtocolState(domain.StateRunning); ok {
		t.Fatal("running mapped to protocol result")
	}
	if _, ok := fromProtocolState("future"); ok {
		t.Fatal("future state mapped")
	}
	if previous := mapPrevious(nil); previous != nil {
		t.Fatal("nil previous mapped")
	}
	if previous := mapPrevious(&executor.Previous{State: domain.StateRunning, Evidence: json.RawMessage(`{}`)}); previous != nil {
		t.Fatal("running previous mapped")
	}
	if previous := mapPrevious(&executor.Previous{State: domain.StateWaitingExternal, Evidence: json.RawMessage(`{`)}); previous != nil {
		t.Fatal("invalid evidence mapped")
	}
	if _, err := mapResult(protocol.DistributionResult{State: "future"}); err == nil {
		t.Fatal("unknown result mapped")
	}
	base := errors.New("base")
	if mapped := mapError(base); !errors.Is(mapped, base) {
		t.Fatalf("mapped=%v", mapped)
	}
}

func TestExecutorReconcilesForcedProviderCrashAfterDispatch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "apply-crashed")
	plan := executorPlan(t, helperConfig{Mode: "process_crash_once", MarkerPath: marker})
	driver := helperDriver(t, "normal")
	engine, err := executor.New(driver, executor.Options{
		MaxAttempts: 3,
		Backoff:     func(uint32) time.Duration { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := domain.NewRunID("run-crash")
	writer, err := journal.OpenWriter(filepath.Join(t.TempDir(), "run.journal"), run)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	state, err := engine.Execute(context.Background(), plan, run, writer)
	if err != nil {
		t.Fatal(err)
	}
	op, ok := state.Operation("publish")
	if !ok || !state.Completed || op.State != domain.StatePublished || op.Attempt != 1 || op.Ambiguous || op.ReconcileRequired {
		t.Fatalf("state=%+v op=%+v", state, op)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("forced-crash marker: %v", err)
	}

	var attempts, dispatches, ambiguous, reconciles, responses int
	for _, event := range writer.Events() {
		switch event.Type {
		case journal.EventAttemptStarted:
			attempts++
		case journal.EventSideEffectDispatched:
			dispatches++
		case journal.EventOutcomeAmbiguous:
			ambiguous++
		case journal.EventReconcileStarted:
			reconciles++
		case journal.EventProviderResponseReceived:
			responses++
		}
	}
	if attempts != 1 || dispatches != 1 || ambiguous != 1 || reconciles != 1 || responses != 1 {
		t.Fatalf("attempts=%d dispatches=%d ambiguous=%d reconciles=%d responses=%d events=%+v",
			attempts, dispatches, ambiguous, reconciles, responses, writer.Events())
	}
}

func TestExecutorRetriesStructuredProviderFailure(t *testing.T) {
	plan := executorPlan(t, helperConfig{Mode: "transient"})
	driver := helperDriver(t, "normal")
	engine, err := executor.New(driver, executor.Options{
		MaxAttempts: 2,
		Backoff:     func(uint32) time.Duration { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := domain.NewRunID("run-transient")
	writer, err := journal.OpenWriter(filepath.Join(t.TempDir(), "run.journal"), run)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	state, err := engine.Execute(context.Background(), plan, run, writer)
	if err != nil {
		t.Fatal(err)
	}
	op, ok := state.Operation("publish")
	if !ok || !state.Completed || op.State != domain.StateFailed || op.Attempt != 2 || !op.Retryable {
		t.Fatalf("state=%+v op=%+v", state, op)
	}
	attempts := 0
	responses := 0
	for _, event := range writer.Events() {
		if event.Type == journal.EventAttemptStarted {
			attempts++
		}
		if event.Type == journal.EventProviderResponseReceived {
			responses++
		}
	}
	if attempts != 2 || responses != 2 {
		t.Fatalf("attempts=%d responses=%d events=%+v", attempts, responses, writer.Events())
	}
}

func executorPlan(t testing.TB, cfg helperConfig) domain.Plan {
	t.Helper()
	digest, err := domain.NewSHA256Digest(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := domain.NewArtifact("app", filepath.Join(t.TempDir(), "app"), digest, 1, "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := domain.NewReleaseID("release-crash")
	release, err := domain.NewRelease(releaseID, []domain.Artifact{artifact})
	if err != nil {
		t.Fatal(err)
	}

	provider := testProviderRef(t)
	configuration, _ := domain.NewJSONValue([]byte(`{}`))
	targetID, _ := domain.NewTargetID("target-a")
	target, err := domain.NewTarget(targetID, provider, configuration)
	if err != nil {
		t.Fatal(err)
	}
	payloadBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := domain.NewJSONValue(payloadBytes)
	if err != nil {
		t.Fatal(err)
	}
	operationID, _ := domain.NewOperationID("publish")
	operation, err := domain.NewOperation(operationID, targetID, provider, "publish", nil, true, "crash-key", time.Second, payload)
	if err != nil {
		t.Fatal(err)
	}
	planID, _ := domain.NewPlanID("plan-crash")
	plan, err := domain.NewPlan(planID, "1", "1", release, []domain.Target{target}, []domain.Operation{operation})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
