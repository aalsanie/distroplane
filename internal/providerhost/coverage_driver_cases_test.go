package providerhost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/executor"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestPreparedInvocationIntegrityAndSingleUse(t *testing.T) {
	request := testExecutorRequest(t, "", true, nil)
	valid := &preparedInvocation{provider: request.Operation.Provider(), operationID: request.Operation.ID(),
		attempt: request.Attempt, operation: executor.DriverApply,
		environment: []string{"SAFE=1"}, redactions: [][]byte{[]byte("secret")}}
	ctx := context.WithValue(context.Background(), preparedInvocationKey{}, valid)
	if got, err := consumePrepared(ctx, request, executor.DriverApply); err != nil || len(got.environment) != 1 {
		t.Fatalf("prepared call rejected: %+v err=%v", got, err)
	}
	if _, err := consumePrepared(ctx, request, executor.DriverApply); err == nil {
		t.Fatal("already consumed invocation accepted")
	}
	valid.release()
	valid.release()
	if len(valid.environment) != 0 || len(valid.redactions) != 0 {
		t.Fatal("released invocation retained secret material")
	}
	var nilInvocation *preparedInvocation
	nilInvocation.release()
	for _, mutation := range []func(*preparedInvocation){
		func(i *preparedInvocation) { i.provider = domain.ProviderRef{} },
		func(i *preparedInvocation) { i.operationID = "other" },
		func(i *preparedInvocation) { i.attempt++ },
		func(i *preparedInvocation) { i.operation = executor.DriverReconcile },
	} {
		prepared := &preparedInvocation{provider: request.Operation.Provider(), operationID: request.Operation.ID(), attempt: request.Attempt, operation: executor.DriverApply}
		mutation(prepared)
		if _, err := consumePrepared(context.WithValue(context.Background(), preparedInvocationKey{}, prepared), request, executor.DriverApply); err == nil {
			t.Fatal("mismatched invocation accepted")
		}
	}
	if _, err := consumePrepared(context.Background(), request, executor.DriverApply); err == nil {
		t.Fatal("unprepared invocation accepted")
	}
	if preparedFromContext(nil) != nil || preparedFromContext(context.Background()) != nil {
		t.Fatal("unexpected prepared invocation")
	}
}

func TestCredentialRequirementsCanonicalMapping(t *testing.T) {
	credential := func(kind, name, environment string) domain.Requirement {
		raw := []byte(nil)
		if environment != "" {
			raw = []byte(`{"environment":"` + environment + `"}`)
		}
		value, err := domain.NewRequirement(kind, name, raw)
		if err != nil {
			t.Fatalf("invalid fixture %q: %v", name, err)
		}
		return value
	}
	first := credential("credential", "key", "Z_TOKEN")
	second := credential("credential", "other", "A_TOKEN")
	nonCredential := credential("scope", "read", "")
	values, err := credentialRequirements([]domain.Requirement{first, nonCredential, second})
	if err != nil || len(values) != 2 || values[0].Environment != "A_TOKEN" || values[1].Environment != "Z_TOKEN" {
		t.Fatalf("non-canonical credential requirements: %+v err=%v", values, err)
	}
	for _, tc := range []struct {
		name   string
		values []domain.Requirement
	}{
		{"missing-env", []domain.Requirement{credential("credential", "key", "")}},
		{"duplicate-env", []domain.Requirement{first, credential("credential", "another", "Z_TOKEN")}},
		{"duplicate-ref", []domain.Requirement{first, credential("credential", "key", "OTHER_TOKEN")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := credentialRequirements(tc.values); err == nil {
				t.Fatal("unacceptable credentials passed through validation")
			}
		})
	}
	if got, err := credentialRequirements(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty credential requirements: %+v %v", got, err)
	}
}

func TestDriverErrorAndPreviousResultBoundaries(t *testing.T) {
	for _, tc := range []struct {
		state    domain.NormalizedState
		evidence json.RawMessage
		valid    bool
	}{
		{domain.StatePublished, json.RawMessage(`{"published":true}`), true},
		{domain.StateRejected, json.RawMessage(`{"rejected":true}`), true},
		{domain.StateWaitingExternal, json.RawMessage(`{}`), true},
		{domain.StateFailed, json.RawMessage(`{}`), false},
		{domain.StatePublished, nil, false},
	} {
		p := mapPrevious(&executor.Previous{State: tc.state, Evidence: tc.evidence})
		if (p != nil) != tc.valid {
			t.Fatalf("previous state=%s mapped=%+v", tc.state, p)
		}
	}
	for _, state := range []protocol.ResultState{protocol.ResultPublished, protocol.ResultRejected, protocol.ResultWaitingExternal} {
		got, err := mapResult(protocol.DistributionResult{State: state, Evidence: json.RawMessage(`{"state":"ok"}`)})
		if err != nil || len(got.Evidence) == 0 {
			t.Fatalf("result=%+v err=%v", got, err)
		}
	}
	if _, err := capabilityFor(executor.DriverOperation(0)); err == nil {
		t.Fatal("unknown driver operation allowed")
	}
	for _, kind := range []executor.DriverOperation{executor.DriverApply, executor.DriverReconcile} {
		if capability, err := capabilityFor(kind); err != nil || capability == "" {
			t.Fatalf("capability=%q err=%v", capability, err)
		}
	}
}
