package executor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestSchedulerHandlesTenThousandOperationDAG(t *testing.T) {
	const count = 10000
	specs := make([]operationSpec, count)
	specs[0] = operationSpec{id: "op-00000"}
	for i := 1; i < count; i++ {
		specs[i] = operationSpec{id: fmt.Sprintf("op-%05d", i), dependencies: []string{"op-00000"}}
	}
	plan := testPlan(t, specs)
	events := []journal.Event{{
		SchemaVersion: journal.SchemaVersion,
		Sequence:      1,
		RunID:         runID(),
		Type:          journal.EventRunStarted,
		ObservedAt:    time.Unix(1, 0).UTC(),
		Payload:       journal.Payload{},
	}}
	state, err := journal.Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	operations := make(map[domain.OperationID]domain.Operation, count)
	for _, operation := range plan.Operations() {
		operations[operation.ID()] = operation
	}
	engine := newExecutor(t, &scriptedDriver{}, Options{MaxConcurrency: 32})
	candidates := engine.candidates(state, operations, map[operationAttempt]struct{}{}, map[operationAttempt]struct{}{})
	if len(candidates) != 1 || candidates[0].operation.ID() != "op-00000" {
		t.Fatalf("initial candidates=%d", len(candidates))
	}

	events = append(events,
		journal.Event{SchemaVersion: journal.SchemaVersion, Sequence: 2, RunID: runID(), Type: journal.EventAttemptStarted, OperationID: "op-00000", TargetID: "target-a", ObservedAt: time.Unix(2, 0).UTC(), Payload: journal.Payload{Attempt: 1, AttemptReason: journal.AttemptReasonInitial}},
		journal.Event{SchemaVersion: journal.SchemaVersion, Sequence: 3, RunID: runID(), Type: journal.EventOperationPublished, OperationID: "op-00000", TargetID: "target-a", ObservedAt: time.Unix(3, 0).UTC(), Payload: journal.Payload{Attempt: 1}},
	)
	state, err = journal.Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	candidates = engine.candidates(state, operations, map[operationAttempt]struct{}{}, map[operationAttempt]struct{}{})
	if len(candidates) != count-1 {
		t.Fatalf("released candidates=%d want=%d", len(candidates), count-1)
	}
}

func TestCancellationStormDoesNotRedispatchSideEffects(t *testing.T) {
	const (
		count          = 128
		maxConcurrency = 16
	)
	specs := make([]operationSpec, count)
	for i := range specs {
		specs[i] = operationSpec{id: fmt.Sprintf("op-%03d", i), sideEffecting: true}
	}
	plan := testPlan(t, specs)
	started := make(chan struct{}, maxConcurrency)
	driver := &scriptedDriver{
		apply: func(ctx context.Context, _ Request) (Result, error) {
			started <- struct{}{}
			<-ctx.Done()
			return Result{}, ctx.Err()
		},
		reconcile: func(context.Context, Request) (Result, error) {
			return published(), nil
		},
	}
	engine := newExecutor(t, driver, Options{MaxConcurrency: maxConcurrency})
	writer := openWriter(t, runID())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := engine.Execute(ctx, plan, runID(), writer)
		done <- err
	}()

	for i := 0; i < maxConcurrency; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatal("initial execution batch did not start")
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancel()
		}()
	}
	wg.Wait()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled execution returned nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled execution did not return")
	}
	if active := driver.active.Load(); active != 0 {
		t.Fatalf("provider calls still active after cancellation: %d", active)
	}

	driver.mu.Lock()
	initialApplyCalls := len(driver.applyCalls)
	driver.mu.Unlock()
	if initialApplyCalls > maxConcurrency {
		t.Fatalf("apply calls=%d exceeded concurrency bound=%d", initialApplyCalls, maxConcurrency)
	}

	state, err := engine.Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Cancelled {
		t.Fatalf("resumed cancelled run lost cancellation state: %+v", state)
	}
	driver.mu.Lock()
	finalApplyCalls := len(driver.applyCalls)
	reconcileCalls := len(driver.reconcileCalls)
	driver.mu.Unlock()
	if finalApplyCalls != initialApplyCalls {
		t.Fatalf("side effects were redispatched after cancellation: before=%d after=%d", initialApplyCalls, finalApplyCalls)
	}
	if reconcileCalls != initialApplyCalls {
		t.Fatalf("reconcile calls=%d want=%d", reconcileCalls, initialApplyCalls)
	}

	dispatched := make(map[domain.OperationID]int)
	for _, event := range writer.Events() {
		if event.Type == journal.EventSideEffectDispatched {
			dispatched[event.OperationID]++
		}
	}
	for operationID, occurrences := range dispatched {
		if occurrences != 1 {
			t.Fatalf("operation %s dispatched %d times", operationID, occurrences)
		}
	}
}

func TestWaitingExternalCanBeReconciledRepeatedlyWithoutRepublish(t *testing.T) {
	plan := testPlan(t, []operationSpec{{id: "op-a", sideEffecting: true}})
	var publishObserved atomic.Bool
	driver := &scriptedDriver{
		apply: func(context.Context, Request) (Result, error) {
			return waiting(), nil
		},
		reconcile: func(context.Context, Request) (Result, error) {
			if publishObserved.Load() {
				return published(), nil
			}
			return waiting(), nil
		},
	}
	engine := newExecutor(t, driver, Options{MaxConcurrency: 1})
	writer := openWriter(t, runID())

	state, err := engine.Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	if state.Completed {
		t.Fatal("waiting run completed prematurely")
	}
	for i := 0; i < 32; i++ {
		state, err = engine.Execute(context.Background(), plan, runID(), writer)
		if err != nil {
			t.Fatal(err)
		}
		if state.Completed {
			t.Fatalf("waiting run completed on reconciliation %d", i)
		}
	}
	driver.mu.Lock()
	applyCalls := len(driver.applyCalls)
	reconcileCalls := len(driver.reconcileCalls)
	driver.mu.Unlock()
	if applyCalls != 1 {
		t.Fatalf("waiting run republished %d times", applyCalls)
	}
	if reconcileCalls != 32 {
		t.Fatalf("reconcile calls=%d want=32", reconcileCalls)
	}

	publishObserved.Store(true)
	state, err = engine.Execute(context.Background(), plan, runID(), writer)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Completed {
		t.Fatalf("externally completed run did not finish: %+v", state)
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.applyCalls) != 1 {
		t.Fatalf("final reconciliation republished side effect: %d", len(driver.applyCalls))
	}
}
