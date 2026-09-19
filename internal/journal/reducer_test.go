package journal

import (
	"reflect"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

func TestReduceRequiredLifecycleEvents(t *testing.T) {
	plan := testPlan(t)
	events := []Event{
		runStarted(1),
		event(2, EventOperationReady, "op-a", "target-a", Payload{}),
		event(3, EventLeaseAcquired, "op-a", "target-a", Payload{}),
		attemptStarted(4, "op-a", "target-a", 1),
		event(5, EventProviderProcessStarted, "op-a", "target-a", Payload{Attempt: 1}),
		dispatched(6, "op-a", "target-a", 1),
		event(7, EventProviderResponseReceived, "op-a", "target-a", Payload{Attempt: 1}),
		event(8, EventOperationPublished, "op-a", "target-a", Payload{Attempt: 1, ProviderState: "published", Evidence: evidence(`{"ref":"a"}`)}),
		event(9, EventOperationReady, "op-b", "target-a", Payload{}),
		event(10, EventLeaseAcquired, "op-b", "target-a", Payload{}),
		event(11, EventLeaseExpired, "op-b", "target-a", Payload{}),
		event(12, EventOperationCancelled, "op-b", "target-a", Payload{ErrorCode: "DEPENDENCY_TERMINAL"}),
		event(13, EventOperationReady, "op-c", "target-b", Payload{}),
		event(14, EventLeaseAcquired, "op-c", "target-b", Payload{}),
		attemptStarted(15, "op-c", "target-b", 1),
		event(16, EventProviderProcessStarted, "op-c", "target-b", Payload{Attempt: 1}),
		dispatched(17, "op-c", "target-b", 1),
		event(18, EventProviderResponseReceived, "op-c", "target-b", Payload{Attempt: 1}),
		event(19, EventOperationWaitingExternal, "op-c", "target-b", Payload{Attempt: 1, ProviderState: "review", Evidence: evidence(`{"ref":"c"}`)}),
		reconcileStarted(20, "op-c", "target-b", 1),
		event(21, EventProviderProcessStarted, "op-c", "target-b", Payload{Attempt: 1}),
		event(22, EventProviderResponseReceived, "op-c", "target-b", Payload{Attempt: 1}),
		event(23, EventOperationPublished, "op-c", "target-b", Payload{Attempt: 1, ProviderState: "published", Evidence: evidence(`{"ref":"c"}`)}),
		event(24, EventRunCompleted, "", "", Payload{}),
	}
	state, err := Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	opA, _ := state.Operation(opID("op-a"))
	if opA.State != domain.StatePublished || opA.LeaseActive || opA.ReconcileRequired {
		t.Fatalf("op-a=%+v", opA)
	}
	opB, _ := state.Operation(opID("op-b"))
	if opB.State != domain.StateCancelled || opB.LeaseActive {
		t.Fatalf("op-b=%+v", opB)
	}
	opC, _ := state.Operation(opID("op-c"))
	if opC.State != domain.StatePublished || opC.LeaseActive || opC.ReconcileRequired {
		t.Fatalf("op-c=%+v", opC)
	}
	if !state.Completed {
		t.Fatalf("state=%+v", state)
	}
}

func TestReduceExplicitOperationResults(t *testing.T) {
	plan := testPlan(t)
	cases := []struct {
		name      string
		eventType EventType
		want      domain.NormalizedState
		dispatch  bool
		retryable bool
	}{
		{"waiting", EventOperationWaitingExternal, domain.StateWaitingExternal, true, true},
		{"published", EventOperationPublished, domain.StatePublished, true, false},
		{"rejected", EventOperationRejected, domain.StateRejected, true, false},
		{"failed", EventOperationFailed, domain.StateFailed, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := []Event{
				runStarted(1),
				attemptStarted(2, "op-c", "target-b", 1),
				event(3, EventProviderProcessStarted, "op-c", "target-b", Payload{Attempt: 1}),
			}
			seq := uint64(4)
			if tc.dispatch {
				events = append(events, dispatched(seq, "op-c", "target-b", 1))
				seq++
			}
			events = append(events,
				event(seq, EventProviderResponseReceived, "op-c", "target-b", Payload{Attempt: 1}),
				event(seq+1, tc.eventType, "op-c", "target-b", Payload{Attempt: 1, Retryable: tc.retryable}),
			)
			state, err := Reduce(plan, events)
			if err != nil {
				t.Fatal(err)
			}
			op, _ := state.Operation(opID("op-c"))
			if op.State != tc.want || op.Retryable != tc.retryable || op.LeaseActive {
				t.Fatalf("op=%+v", op)
			}
		})
	}
}

func TestReduceLifecycleAndTargetAggregation(t *testing.T) {
	plan := testPlan(t)
	events := []Event{
		runStarted(1),
		attemptStarted(2, "op-a", "target-a", 1),
		dispatched(3, "op-a", "target-a", 1),
		event(4, EventOperationResult, "op-a", "target-a", Payload{Attempt: 1, State: domain.StatePublished, ProviderState: "published", Evidence: evidence(`{"ref":"a"}`)}),
		attemptStarted(5, "op-b", "target-a", 1),
		result(6, "op-b", "target-a", 1, domain.StatePublished),
		attemptStarted(7, "op-c", "target-b", 1),
		dispatched(8, "op-c", "target-b", 1),
		event(9, EventOperationResult, "op-c", "target-b", Payload{Attempt: 1, State: domain.StateWaitingExternal, ProviderState: "review", Evidence: evidence(`{"url":"https://example.invalid/review"}`)}),
		reconcileStarted(10, "op-c", "target-b", 1),
		event(11, EventReconcileResult, "op-c", "target-b", Payload{Attempt: 1, State: domain.StatePublished, ProviderState: "published", Evidence: evidence(`{"ref":"c"}`)}),
	}
	state, err := Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Started || state.RunID != runID() {
		t.Fatalf("state=%+v", state)
	}
	for _, id := range []string{"op-a", "op-b", "op-c"} {
		op, ok := state.Operation(opID(id))
		if !ok {
			t.Fatalf("missing %s", id)
		}
		if op.State != domain.StatePublished || op.ReconcileRequired || op.Ambiguous {
			t.Fatalf("%s=%+v", id, op)
		}
	}
	for _, id := range []string{"target-a", "target-b", "target-noop"} {
		target, ok := state.Target(targetID(id))
		if !ok {
			t.Fatalf("missing %s", id)
		}
		if target.State != domain.StatePublished || target.ReconcileRequired || target.Ambiguous {
			t.Fatalf("%s=%+v", id, target)
		}
	}
	opA, _ := state.Operation(opID("op-a"))
	copyEvidence := opA.Evidence
	copyEvidence[0] = '['
	opA2, _ := state.Operation(opID("op-a"))
	if len(opA2.Evidence) == 0 || opA2.Evidence[0] != '{' {
		t.Fatal("evidence was not defensively copied")
	}
	operations := state.Operations()
	operations[0].Evidence = nil
	if again, _ := state.Operation(operations[0].ID); len(again.Evidence) == 0 {
		t.Fatal("operations slice mutated state")
	}
	if targets := state.Targets(); len(targets) != 3 || targets[0].ID != targetID("target-a") {
		t.Fatalf("targets=%+v", targets)
	}
	if _, ok := state.Operation(opID("missing")); ok {
		t.Fatal("unknown operation found")
	}
	if _, ok := state.Target(targetID("missing")); ok {
		t.Fatal("unknown target found")
	}
}

func TestReduceInitialAndReadyStates(t *testing.T) {
	plan := testPlan(t)
	initial, err := Reduce(plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Started || initial.RunID != "" {
		t.Fatalf("initial=%+v", initial)
	}
	for _, op := range initial.Operations() {
		if op.State != domain.StatePlanned {
			t.Fatalf("op=%+v", op)
		}
	}
	for _, target := range initial.Targets() {
		if target.State != domain.StatePlanned {
			t.Fatalf("target=%+v", target)
		}
	}
	started, err := Reduce(plan, []Event{runStarted(1)})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]domain.NormalizedState{"op-a": domain.StateReady, "op-b": domain.StatePlanned, "op-c": domain.StateReady} {
		got, _ := started.Operation(opID(id))
		if got.State != want {
			t.Fatalf("%s=%s want %s", id, got.State, want)
		}
	}
	noop, _ := started.Target(targetID("target-noop"))
	if noop.State != domain.StatePublished {
		t.Fatalf("noop=%+v", noop)
	}
}

func TestReduceReconcileResultStates(t *testing.T) {
	plan := testPlan(t)
	for _, want := range []domain.NormalizedState{
		domain.StateWaitingExternal,
		domain.StatePublished,
		domain.StateRejected,
		domain.StateFailed,
		domain.StateCancelled,
	} {
		t.Run(string(want), func(t *testing.T) {
			events := []Event{
				runStarted(1),
				attemptStarted(2, "op-c", "target-b", 1),
				dispatched(3, "op-c", "target-b", 1),
				reconcileStarted(4, "op-c", "target-b", 1),
				reconcileResult(5, "op-c", "target-b", 1, want),
			}
			state, err := Reduce(plan, events)
			if err != nil {
				t.Fatal(err)
			}
			op, _ := state.Operation(opID("op-c"))
			if op.State != want {
				t.Fatalf("op=%+v", op)
			}
			if (want == domain.StateWaitingExternal) != op.ReconcileRequired {
				t.Fatalf("op=%+v", op)
			}
			if (want == domain.StateWaitingExternal) != op.Ambiguous {
				t.Fatalf("op=%+v", op)
			}
		})
	}
}

func TestReduceFailureRetryAndExplicitAmbiguity(t *testing.T) {
	plan := testPlan(t)
	events := []Event{
		runStarted(1),
		attemptStarted(2, "op-c", "target-b", 1),
		dispatched(3, "op-c", "target-b", 1),
		event(4, EventOperationResult, "op-c", "target-b", Payload{Attempt: 1, State: domain.StateFailed, ErrorCode: "TRANSIENT_EXTERNAL_ERROR", Retryable: true}),
		attemptStarted(5, "op-c", "target-b", 2),
		dispatched(6, "op-c", "target-b", 2),
		event(7, EventOutcomeAmbiguous, "op-c", "target-b", Payload{Attempt: 2, ErrorCode: "AMBIGUOUS_OUTCOME"}),
		reconcileStarted(8, "op-c", "target-b", 2),
		reconcileResult(9, "op-c", "target-b", 2, domain.StatePublished),
	}
	state, err := Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation(opID("op-c"))
	if op.State != domain.StatePublished || op.Attempt != 2 || op.Ambiguous || op.ReconcileRequired {
		t.Fatalf("op=%+v", op)
	}
}

func TestCrashRecoveryAmbiguity(t *testing.T) {
	plan := testPlan(t)
	cases := []struct {
		name      string
		events    []Event
		state     domain.NormalizedState
		ambiguous bool
		reconcile bool
	}{
		{"before dispatch", []Event{runStarted(1), attemptStarted(2, "op-a", "target-a", 1)}, domain.StateRunning, false, false},
		{"after dispatch before response", []Event{runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1)}, domain.StateRunning, true, true},
		{"after response before success sync", []Event{runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1)}, domain.StateRunning, true, true},
		{"after durable success", []Event{runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), result(4, "op-a", "target-a", 1, domain.StatePublished)}, domain.StatePublished, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, err := Reduce(plan, tc.events)
			if err != nil {
				t.Fatal(err)
			}
			op, _ := state.Operation(opID("op-a"))
			if op.State != tc.state || op.Ambiguous != tc.ambiguous || op.ReconcileRequired != tc.reconcile {
				t.Fatalf("op=%+v", op)
			}
			target, _ := state.Target(targetID("target-a"))
			if target.Ambiguous != tc.ambiguous || target.ReconcileRequired != tc.reconcile {
				t.Fatalf("target=%+v", target)
			}
		})
	}
}

func TestReduceTargetFailurePrecedence(t *testing.T) {
	plan := testPlan(t)
	cases := []struct {
		state domain.NormalizedState
		want  domain.NormalizedState
	}{
		{domain.StateRejected, domain.StateRejected},
		{domain.StateFailed, domain.StateFailed},
		{domain.StateCancelled, domain.StateCancelled},
		{domain.StateWaitingExternal, domain.StateWaitingExternal},
	}
	for _, tc := range cases {
		events := []Event{runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), result(4, "op-a", "target-a", 1, tc.state)}
		state, err := Reduce(plan, events)
		if err != nil {
			t.Fatal(err)
		}
		target, _ := state.Target(targetID("target-a"))
		if target.State != tc.want {
			t.Fatalf("state=%s target=%+v", tc.state, target)
		}
	}
}

func TestReduceRejectsInvalidTransitions(t *testing.T) {
	plan := testPlan(t)
	otherRun := runStarted(2)
	otherRun.RunID = "run-2"
	cases := map[string][]Event{
		"event before run":                 {attemptStarted(1, "op-a", "target-a", 1)},
		"duplicate run":                    {runStarted(1), runStarted(2)},
		"duplicate ready":                  {runStarted(1), event(2, EventOperationReady, "op-a", "target-a", Payload{}), event(3, EventOperationReady, "op-a", "target-a", Payload{})},
		"ready before dependencies":        {runStarted(1), event(2, EventOperationReady, "op-b", "target-a", Payload{})},
		"lease before ready":               {runStarted(1), event(2, EventLeaseAcquired, "op-b", "target-a", Payload{})},
		"duplicate lease":                  {runStarted(1), event(2, EventLeaseAcquired, "op-a", "target-a", Payload{}), event(3, EventLeaseAcquired, "op-a", "target-a", Payload{})},
		"lease expiry without acquire":     {runStarted(1), event(2, EventLeaseExpired, "op-a", "target-a", Payload{})},
		"provider start without attempt":   {runStarted(1), event(2, EventProviderProcessStarted, "op-a", "target-a", Payload{Attempt: 1})},
		"provider response without start":  {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), event(3, EventProviderResponseReceived, "op-a", "target-a", Payload{Attempt: 1})},
		"provider response wrong attempt":  {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), event(3, EventProviderProcessStarted, "op-a", "target-a", Payload{Attempt: 1}), event(4, EventProviderResponseReceived, "op-a", "target-a", Payload{Attempt: 2})},
		"duplicate sequence":               {runStarted(1), attemptStarted(1, "op-a", "target-a", 1)},
		"sequence gap":                     {runStarted(1), attemptStarted(3, "op-a", "target-a", 1)},
		"multiple runs":                    {runStarted(1), otherRun},
		"unknown operation":                {runStarted(1), attemptStarted(2, "missing", "target-a", 1)},
		"target mismatch":                  {runStarted(1), attemptStarted(2, "op-a", "target-b", 1)},
		"dependency not ready":             {runStarted(1), attemptStarted(2, "op-b", "target-a", 1)},
		"attempt gap":                      {runStarted(1), attemptStarted(2, "op-a", "target-a", 2)},
		"dispatch non side effect":         {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), result(4, "op-a", "target-a", 1, domain.StatePublished), attemptStarted(5, "op-b", "target-a", 1), dispatched(6, "op-b", "target-a", 1)},
		"dispatch without attempt":         {runStarted(1), dispatched(2, "op-a", "target-a", 1)},
		"duplicate provider start":         {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), event(3, EventProviderProcessStarted, "op-a", "target-a", Payload{Attempt: 1}), event(4, EventProviderProcessStarted, "op-a", "target-a", Payload{Attempt: 1})},
		"duplicate provider response":      {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), event(3, EventProviderProcessStarted, "op-a", "target-a", Payload{Attempt: 1}), event(4, EventProviderResponseReceived, "op-a", "target-a", Payload{Attempt: 1}), event(5, EventProviderResponseReceived, "op-a", "target-a", Payload{Attempt: 1})},
		"duplicate dispatch":               {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), dispatched(4, "op-a", "target-a", 1)},
		"side result before dispatch":      {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), result(3, "op-a", "target-a", 1, domain.StatePublished)},
		"explicit result before dispatch":  {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), event(3, EventOperationPublished, "op-a", "target-a", Payload{Attempt: 1})},
		"result wrong attempt":             {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), result(4, "op-a", "target-a", 2, domain.StatePublished)},
		"retry while ambiguous":            {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), attemptStarted(4, "op-a", "target-a", 2)},
		"ambiguous non side effect":        {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), result(4, "op-a", "target-a", 1, domain.StatePublished), attemptStarted(5, "op-b", "target-a", 1), event(6, EventOutcomeAmbiguous, "op-b", "target-a", Payload{Attempt: 1})},
		"ambiguous before dispatch":        {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), event(3, EventOutcomeAmbiguous, "op-a", "target-a", Payload{Attempt: 1})},
		"reconcile not required":           {runStarted(1), reconcileStarted(2, "op-a", "target-a", 0)},
		"reconcile wrong attempt":          {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), reconcileStarted(4, "op-a", "target-a", 2)},
		"explicit reconcile wrong attempt": {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), reconcileStarted(4, "op-a", "target-a", 1), event(5, EventOperationPublished, "op-a", "target-a", Payload{Attempt: 2})},
		"reconcile result without start":   {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), reconcileResult(4, "op-a", "target-a", 1, domain.StatePublished)},
		"start after published":            {runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), result(4, "op-a", "target-a", 1, domain.StatePublished), attemptStarted(5, "op-a", "target-a", 2)},
	}
	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Reduce(plan, events); err == nil {
				t.Fatal("invalid transition accepted")
			}
		})
	}
}

func TestReduceDeterministicReplay(t *testing.T) {
	plan := testPlan(t)
	events := []Event{runStarted(1), attemptStarted(2, "op-a", "target-a", 1), dispatched(3, "op-a", "target-a", 1), result(4, "op-a", "target-a", 1, domain.StatePublished)}
	first, err := Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Operations(), second.Operations()) || !reflect.DeepEqual(first.Targets(), second.Targets()) {
		t.Fatal("replay is not deterministic")
	}
}

func TestReduceOneHundredThousandEvents(t *testing.T) {
	plan := testPlan(t)
	events := make([]Event, 0, 100000)
	events = append(events, runStarted(1))
	seq := uint64(2)
	for attempt := uint32(1); attempt <= 33333; attempt++ {
		events = append(events,
			attemptStarted(seq, "op-c", "target-b", attempt),
			dispatched(seq+1, "op-c", "target-b", attempt),
			event(seq+2, EventOperationResult, "op-c", "target-b", Payload{Attempt: attempt, State: domain.StateFailed, Retryable: true}),
		)
		seq += 3
	}
	if len(events) != 100000 {
		t.Fatalf("events=%d", len(events))
	}
	state, err := Reduce(plan, events)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := state.Operation(opID("op-c"))
	if op.State != domain.StateFailed || op.Attempt != 33333 {
		t.Fatalf("op=%+v", op)
	}
}

func FuzzReduce(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6})
	f.Add([]byte{1, 1, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		plan := testPlan(t)
		events := []Event{runStarted(1)}
		seq := uint64(2)
		for _, b := range data {
			if len(events) > 64 {
				break
			}
			switch b % 7 {
			case 0:
				events = append(events, attemptStarted(seq, "op-a", "target-a", 1))
			case 1:
				events = append(events, dispatched(seq, "op-a", "target-a", 1))
			case 2:
				events = append(events, result(seq, "op-a", "target-a", 1, domain.StateFailed))
			case 3:
				events = append(events, event(seq, EventOutcomeAmbiguous, "op-a", "target-a", Payload{Attempt: 1, ErrorCode: "AMBIGUOUS_OUTCOME"}))
			case 4:
				events = append(events, reconcileStarted(seq, "op-a", "target-a", 1))
			case 5:
				events = append(events, reconcileResult(seq, "op-a", "target-a", 1, domain.StatePublished))
			case 6:
				events = append(events, attemptStarted(seq, "op-b", "target-a", 1))
			}
			seq++
		}
		_, _ = Reduce(plan, events)
	})
}
