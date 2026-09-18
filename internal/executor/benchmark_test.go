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
