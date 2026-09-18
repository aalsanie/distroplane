package journal

import (
	"bytes"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

func BenchmarkEncodeFrame(b *testing.B) {
	event := result(4, "op-a", "target-a", 1, domain.StatePublished)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := encodeFrame(event); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadTenThousandEvents(b *testing.B) {
	data := benchmarkJournal(b, 10000)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Read(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReduceTenThousandEvents(b *testing.B) {
	plan := testPlan(b)
	events := benchmarkEvents(10000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Reduce(plan, events); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReduceOneHundredThousandEvents(b *testing.B) {
	plan := testPlan(b)
	events := benchmarkEvents(100000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Reduce(plan, events); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkJournal(tb testing.TB, count int) []byte {
	tb.Helper()
	var out bytes.Buffer
	for _, event := range benchmarkEvents(count) {
		frame, err := encodeFrame(event)
		if err != nil {
			tb.Fatal(err)
		}
		out.Write(frame)
	}
	return out.Bytes()
}

func benchmarkEvents(count int) []Event {
	if count < 1 {
		return nil
	}
	events := make([]Event, 0, count)
	events = append(events, runStarted(1))
	seq := uint64(2)
	attempt := uint32(1)
	for len(events)+3 <= count {
		events = append(events,
			attemptStarted(seq, "op-c", "target-b", attempt),
			dispatched(seq+1, "op-c", "target-b", attempt),
			event(seq+2, EventOperationResult, "op-c", "target-b", Payload{Attempt: attempt, State: domain.StateFailed, Retryable: true}),
		)
		seq += 3
		attempt++
	}
	return events
}
