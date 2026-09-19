package executor

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync/atomic"
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


func BenchmarkExecuteOneThousandOperationsPeakMemory(b *testing.B) {
	operations := make([]operationSpec, 1000)
	for i := range operations {
		operations[i] = operationSpec{id: fmt.Sprintf("op-%04d", i)}
	}
	plan := testPlan(b, operations)
	engine := newExecutor(b, &scriptedDriver{}, Options{MaxConcurrency: 32, Backoff: func(uint32) time.Duration { return 0 }})
	var observedPeak atomic.Uint64
	var observedGoroutines atomic.Int64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		runtime.GC()
		run, err := domain.NewRunID(fmt.Sprintf("run-peak-%d", i+1))
		if err != nil {
			b.Fatal(err)
		}
		writer, err := journal.OpenWriter(filepath.Join(b.TempDir(), "run.journal"), run)
		if err != nil {
			b.Fatal(err)
		}
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			var stats runtime.MemStats
			for {
				runtime.ReadMemStats(&stats)
				for {
					current := observedPeak.Load()
					if stats.HeapAlloc <= current || observedPeak.CompareAndSwap(current, stats.HeapAlloc) {
						break
					}
				}
				goroutines := int64(runtime.NumGoroutine())
				for {
					current := observedGoroutines.Load()
					if goroutines <= current || observedGoroutines.CompareAndSwap(current, goroutines) {
						break
					}
				}
				select {
				case <-stop:
					return
				default:
					runtime.Gosched()
				}
			}
		}()
		b.StartTimer()
		state, err := engine.Execute(context.Background(), plan, run, writer)
		b.StopTimer()
		close(stop)
		<-done
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
	b.StopTimer()
	b.ReportMetric(float64(observedPeak.Load()), "peak-heap-B")
	b.ReportMetric(float64(observedGoroutines.Load()), "peak-goroutines")
}
