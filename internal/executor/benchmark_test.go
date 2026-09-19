package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func BenchmarkExecuteOneThousandOperations(b *testing.B) {
	operations := make([]operationSpec, 1000)
	for i := range operations {
		operations[i] = operationSpec{id: fmt.Sprintf("op-%04d", i)}
	}
	plan := testPlan(b, operations)
	executor := newExecutor(b, &scriptedDriver{}, Options{MaxConcurrency: 32, Backoff: func(uint32) time.Duration { return 0 }})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		run, err := domain.NewRunID(fmt.Sprintf("run-%d", i+1))
		if err != nil {
			b.Fatal(err)
		}
		writer, err := journal.OpenWriter(filepath.Join(b.TempDir(), "run.journal"), run)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		state, err := executor.Execute(context.Background(), plan, run, writer)
		b.StopTimer()
		if closeErr := writer.Close(); closeErr != nil {
			b.Fatal(closeErr)
		}
		if err != nil {
			b.Fatal(err)
		}
		if !state.Completed {
			b.Fatal("run did not complete")
		}
		b.StartTimer()
	}
}

func BenchmarkSchedulerOneThousandOperations(b *testing.B) {
	benchmarkScheduler(b, 1000)
}

func BenchmarkSchedulerTenThousandOperations(b *testing.B) {
	benchmarkScheduler(b, 10000)
}

func benchmarkScheduler(b *testing.B, count int) {
	operations := make([]operationSpec, count)
	for i := range operations {
		operations[i] = operationSpec{id: fmt.Sprintf("op-%05d", i)}
	}
	plan := testPlan(b, operations)
	state, err := journal.Reduce(plan, []journal.Event{{
		SchemaVersion: journal.SchemaVersion,
		Sequence:      1,
		RunID:         runID(),
		Type:          journal.EventRunStarted,
		ObservedAt:    time.Unix(1, 0).UTC(),
		Payload:       journal.Payload{},
	}})
	if err != nil {
		b.Fatal(err)
	}
	engine := newExecutor(b, &scriptedDriver{}, Options{MaxConcurrency: 32})
	operationMap := make(map[domain.OperationID]domain.Operation, len(plan.Operations()))
	for _, operation := range plan.Operations() {
		operationMap[operation.ID()] = operation
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		candidates := engine.candidates(state, operationMap, map[operationAttempt]struct{}{}, map[operationAttempt]struct{}{})
		if len(candidates) != count {
			b.Fatalf("candidates=%d want=%d", len(candidates), count)
		}
	}
}
