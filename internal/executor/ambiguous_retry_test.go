package executor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestAmbiguousRecoveryCannotAuthorizeNewRetry(t *testing.T) {
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
	entry := journal.Entry{RunID: runID(), Type: journal.EventRetryAuthorized, OperationID: "op-a", TargetID: "target-a", Payload: journal.Payload{Attempt: 1}}
	if _, err := writer.Append(entry); !errors.Is(err, journal.ErrRetryAuthorizationDisabled) {
		t.Fatalf("authorization was not refused: %v", err)
	}
	state, err = engine.Execute(context.Background(), plan, runID(), writer)
	if err != nil || state.Completed || publishCalls != 1 {
		t.Fatalf("refused authorization changed recovery: state=%+v err=%v calls=%d", state, err, publishCalls)
	}
	events := writer.Events()
	for _, event := range events {
		if event.Type == journal.EventRetryAuthorized {
			t.Fatal("new authorization persisted")
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	prefix, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := func(seq uint64, kind journal.EventType, attempt uint32) []byte {
		t.Helper()
		event := journal.Event{
			SchemaVersion: journal.SchemaVersion, Sequence: seq, RunID: runID(),
			Type: kind, OperationID: "op-a", TargetID: "target-a", ObservedAt: time.Now().UTC(),
			Payload: journal.Payload{Attempt: attempt},
		}
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		frame := make([]byte, 12+len(payload))
		copy(frame, "DPJ1")
		binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
		binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(payload))
		copy(frame[12:], payload)
		return frame
	}
	next := uint64(len(events) + 1)
	frames := [][]byte{
		legacy(next, journal.EventRetryAuthorized, 1),
		legacy(next+1, journal.EventAttemptStarted, 2),
		legacy(next+2, journal.EventProviderProcessStarted, 2),
		legacy(next+3, journal.EventSideEffectDispatched, 2),
	}
	for i, frame := range frames {
		prefix = append(prefix, frame...)
		t.Run(framesName(i), func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "legacy.journal")
			if err := os.WriteFile(file, prefix, 0600); err != nil {
				t.Fatal(err)
			}
			opened, err := journal.OpenWriter(file, runID())
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			probe := &scriptedDriver{}
			state, err := newExecutor(t, probe, Options{MaxAttempts: 1}).Execute(context.Background(), plan, runID(), opened)
			if i < 3 {
				if !errors.Is(err, journal.ErrLegacyRetryPending) || len(probe.applyCalls) != 0 || len(probe.reconcileCalls) != 0 {
					t.Fatalf("unconsumed grant executed: state=%+v err=%v", state, err)
				}
				after, err := os.ReadFile(file)
				if err != nil || !bytes.Equal(prefix, after) {
					t.Fatalf("refused execution changed historical bytes: %v", err)
				}
			} else {
				op, _ := state.Operation("op-a")
				if err != nil || !state.Completed || op.Attempt != 2 || len(probe.applyCalls) != 0 || len(probe.reconcileCalls) != 1 {
					t.Fatalf("dispatched historical grant not recovered by observation: state=%+v err=%v", state, err)
				}
				if probe.reconcileCalls[0].Operation.IdempotencyKey() != "key-a" {
					t.Fatal("historical recovery changed idempotency key")
				}
			}
		})
	}
}

func framesName(index int) string {
	return []string{"grant", "attempt", "provider-started", "dispatched"}[index]
}
