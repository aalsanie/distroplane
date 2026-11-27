package providerhost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestProviderPhaseDeadlinePolicy(t *testing.T) {
	client, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if client.waitDelay != 2*time.Second {
		t.Fatalf("pipe drain limit=%s", client.waitDelay)
	}
	for _, tc := range []struct {
		phase protocol.Operation
		limit time.Duration
	}{
		{protocol.OperationDescribe, 10 * time.Second},
		{protocol.OperationPlan, 60 * time.Second},
		{protocol.OperationApply, domain.DefaultOperationTimeout},
		{protocol.OperationReconcile, domain.DefaultOperationTimeout},
	} {
		t.Run(string(tc.phase), func(t *testing.T) {
			start := time.Now()
			ctx, cancel := client.phaseContext(context.Background(), tc.phase)
			deadline, ok := ctx.Deadline()
			cancel()
			if !ok || deadline.Before(start.Add(tc.limit)) || deadline.After(time.Now().Add(tc.limit)) {
				t.Fatalf("deadline=%v expected limit=%s", deadline, tc.limit)
			}

			parent, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			ctx, cancel = client.phaseContext(parent, tc.phase)
			defer cancel()
			want, _ := parent.Deadline()
			got, ok := ctx.Deadline()
			if !ok || !got.Equal(want) {
				t.Fatalf("earlier deadline changed: %v want %v", got, want)
			}
			stop()
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("parent cancellation lost: %v", ctx.Err())
			}
		})
	}
	for _, options := range []Options{{DescribeTimeout: -time.Second}, {PlanTimeout: -time.Second}} {
		if _, err := New(options); err == nil {
			t.Fatalf("negative phase timeout accepted: %+v", options)
		}
	}
	override, err := New(Options{DescribeTimeout: 20 * time.Second, PlanTimeout: 90 * time.Second, WaitDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if override.describeTimeout != 20*time.Second || override.planTimeout != 90*time.Second || override.waitDelay != time.Second {
		t.Fatalf("explicit phase limits lost: %+v", override)
	}
	parent, stop := context.WithTimeout(context.Background(), 5*time.Minute)
	defer stop()
	for _, phase := range []protocol.Operation{protocol.OperationApply, protocol.OperationReconcile} {
		ctx, cancel := client.phaseContext(parent, phase)
		want, _ := parent.Deadline()
		got, ok := ctx.Deadline()
		cancel()
		if !ok || !got.Equal(want) {
			t.Fatalf("%s shortened explicit operation timeout: %v want %v", phase, got, want)
		}
		if ctx != parent || parent.Err() != nil {
			t.Fatalf("%s must use the caller deadline directly without cancelling the caller", phase)
		}
	}
}

func TestProviderPhaseTimeoutStopsBlockedIO(t *testing.T) {
	for _, phase := range []protocol.Operation{protocol.OperationDescribe, protocol.OperationPlan, protocol.OperationApply, protocol.OperationReconcile} {
		t.Run(string(phase), func(t *testing.T) {
			client := helperClient(t, "sleep", func(options *Options) {
				options.DescribeTimeout = 250 * time.Millisecond
				options.PlanTimeout = 250 * time.Millisecond
			})
			parent := context.Background()
			if phase == protocol.OperationApply || phase == protocol.OperationReconcile {
				ctx, cancel := context.WithTimeout(parent, 250*time.Millisecond)
				defer cancel()
				parent = ctx
			}
			endpoint := helperEndpoint(t)
			start := time.Now()
			var err error
			switch phase {
			case protocol.OperationDescribe:
				_, err = client.Describe(parent, endpoint)
			case protocol.OperationPlan:
				request := planRequest("")
				request.Target.Configuration = []byte("{\"padding\":\"" + strings.Repeat("x", 512<<10) + "\"}")
				_, err = client.Plan(parent, endpoint, request)
			case protocol.OperationApply:
				_, err = client.apply(parent, endpoint.Executable, protocol.ApplyRequest{
					PlanID: "plan", TargetID: "target", OperationID: "op", IdempotencyKey: "key", Attempt: 1,
					ProviderPayload: []byte("{\"padding\":\"" + strings.Repeat("x", 512<<10) + "\"}"),
				})
			case protocol.OperationReconcile:
				_, err = client.reconcile(parent, endpoint.Executable, protocol.ReconcileRequest{
					PlanID: "plan", TargetID: "target", OperationID: "op", IdempotencyKey: "key", Attempt: 1,
					ProviderPayload: []byte("{}"),
				})
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s: %v", phase, err)
			}
			if elapsed := time.Since(start); elapsed > 8*time.Second {
				t.Fatalf("%s timeout cleanup took %s", phase, elapsed)
			}
			healthy := helperClient(t, "normal", nil)
			if _, err := healthy.Describe(context.Background(), endpoint); err != nil {
				t.Fatalf("invocation after timeout: %v", err)
			}
		})
	}
}
