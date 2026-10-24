package executor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func npmExecutionPlan(t *testing.T) domain.Plan {
	t.Helper()
	original := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	name, _ := domain.NewProviderName("npm")
	version, _ := domain.NewProviderVersion("1")
	provider, err := domain.NewProviderRef(name, version)
	if err != nil {
		t.Fatal(err)
	}
	config, _ := domain.NewJSONValue([]byte(`{}`))
	payload, _ := domain.NewJSONValue([]byte(`{}`))
	target, err := domain.NewTarget("target-a", provider, config)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := domain.NewOperation("op-a", "target-a", provider, "publish", nil, true, "key-a", 0, payload)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := domain.NewPlan(original.ID(), original.SchemaVersion(), original.ProtocolVersion(), original.Release(), []domain.Target{target}, []domain.Operation{operation})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestAmbiguousNpmRecoveryRequiresExplicitAuthorization(t *testing.T) {
	plan := npmExecutionPlan(t)
	path := filepath.Join(t.TempDir(), "run.journal")
	writer, err := journal.OpenWriter(path, runID())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	publishCalls := 0
	driver := &scriptedDriver{
		apply: func(context.Context, Request) (Result, error) {
			publishCalls++
			if publishCalls == 1 {
				return Result{}, &DriverError{Code: "AMBIGUOUS_OUTCOME", Ambiguous: true}
			}
			return published(), nil
		},
		reconcile: func(context.Context, Request) (Result, error) {
			return Result{State: domain.StateWaitingExternal, ProviderState: "absent", Evidence: []byte(`{"publicationState":"absent"}`)}, nil
		},
	}
	engine := newExecutor(t, driver, Options{MaxAttempts: 1})
	state, err := engine.Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	operation, _ := state.Operation("op-a")
	if state.Completed || !operation.Ambiguous || operation.State != domain.StateWaitingExternal {
		t.Fatalf("unexpected ambiguous state=%+v op=%+v", state, operation)
	}
	for i := 0; i < 3; i++ {
		state, err = engine.Execute(context.Background(), plan, runID(), writer)
		if err != nil {
			t.Fatal(err)
		}
	}
	if publishCalls != 1 {
		t.Fatalf("ordinary apply republished ambiguous side effect %d times", publishCalls)
	}
	if _, err := writer.AuthorizeAmbiguousRetry(plan, runID(), "op-a"); err != nil {
		t.Fatal(err)
	}
	state, err = engine.Execute(context.Background(), plan, runID(), writer)
	if err != nil || !state.Completed {
		t.Fatalf("authorized retry state=%+v err=%v", state, err)
	}
	if publishCalls != 2 {
		t.Fatalf("authorized retry performed %d publishes, want 2 total", publishCalls)
	}
	authorizedCount := 0
	for _, event := range writer.Events() {
		if event.Type == journal.EventRetryAuthorized {
			authorizedCount++
		}
	}
	if authorizedCount != 1 {
		t.Fatalf("retry authorizations=%d, want 1", authorizedCount)
	}
	state, err = engine.Execute(context.Background(), plan, runID(), writer)
	if err != nil || !state.Completed || publishCalls != 2 {
		t.Fatalf("completed run replayed publication: state=%+v err=%v calls=%d", state, err, publishCalls)
	}
}
