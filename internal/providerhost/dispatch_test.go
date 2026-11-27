package providerhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/executor"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/protocol"
)

func TestGatedReaderDecision(t *testing.T) {
	for _, mode := range []string{"commit", "abort", "cancelled", "race"} {
		t.Run(mode, func(t *testing.T) {
			for range 100 {
				ctx, cancel := context.WithCancel(context.Background())
				gate := &gatedReader{reader: strings.NewReader("apply"), gate: make(chan struct{})}
				type readResult struct {
					data []byte
					err  error
				}
				read := make(chan readResult, 1)
				go func() { data, err := io.ReadAll(gate); read <- readResult{data, err} }()
				var committed bool
				switch mode {
				case "commit":
					committed = gate.commit(ctx)
					if !committed {
						t.Fatal("commit denied")
					}
					cancel()
					gate.abort()
				case "abort":
					gate.abort()
					committed = gate.commit(ctx)
					if committed {
						t.Fatal("aborted input committed")
					}
				case "cancelled":
					cancel()
					committed = gate.commit(ctx)
					if committed {
						t.Fatal("cancelled input committed")
					}
				case "race":
					var wg sync.WaitGroup
					start := make(chan struct{})
					wg.Add(2)
					go func() { defer wg.Done(); <-start; cancel(); gate.abort() }()
					go func() { defer wg.Done(); <-start; committed = gate.commit(ctx) }()
					close(start)
					wg.Wait()
				}
				cancel()
				gate.abort()
				if gate.commit(ctx) != committed {
					t.Fatal("decision changed")
				}
				want := ""
				if committed {
					want = "apply"
				}
				select {
				case got := <-read:
					if got.err != nil || string(got.data) != want {
						t.Fatalf("input=%q want=%q err=%v", got.data, want, got.err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("stdin reader remained blocked")
				}
				if n, err := gate.Read(make([]byte, 1)); n != 0 || err != io.EOF {
					t.Fatalf("second read: %d %v", n, err)
				}
			}
		})
	}
}

const (
	dispatchCaptureEnv = "DISTROPLANE_TEST_DISPATCH_CAPTURE"
	dispatchEffectEnv  = "DISTROPLANE_TEST_DISPATCH_EFFECT"
	dispatchModeEnv    = "DISTROPLANE_TEST_DISPATCH_MODE"
)

type dispatchRecordingHandler struct {
	helperHandler
	marker string
}

func (h dispatchRecordingHandler) Apply(ctx context.Context, request protocol.ApplyRequest) (protocol.ApplyResponse, *protocol.ProviderError) {
	file, err := os.OpenFile(h.marker, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(71)
	}
	if _, err := file.WriteString("apply\n"); err != nil {
		os.Exit(71)
	}
	if err := file.Close(); err != nil {
		os.Exit(71)
	}
	return h.helperHandler.Apply(ctx, request)
}

func TestDispatchInputHelper(t *testing.T) {
	path := os.Getenv(dispatchCaptureEnv)
	if path == "" {
		return
	}
	// Creating the capture file is the readiness signal. Every byte consumed from
	// stdin is written to it before the protocol handler can perform an effect.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(71)
	}
	switch os.Getenv(dispatchModeEnv) {
	case "exit":
		_ = file.Close()
		os.Exit(70)
	case "blocked":
		time.Sleep(30 * time.Second)
		_ = file.Close()
		os.Exit(70)
	}
	err = protocol.ServeOnce(context.Background(), io.TeeReader(os.Stdin, file), os.Stdout,
		dispatchRecordingHandler{marker: os.Getenv(dispatchEffectEnv)}, protocol.NewCodec(protocol.DefaultMaxMessageBytes))
	if closeErr := file.Close(); closeErr != nil {
		os.Exit(71)
	}
	if err != nil {
		os.Exit(70)
	}
	os.Exit(0)
}

func waitForEmptyDispatchCapture(ctx context.Context, path string) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := os.Stat(path)
		if err == nil {
			if info.Size() != 0 {
				return fmt.Errorf("request reached provider before dispatch")
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func TestProviderInputDispatch(t *testing.T) {
	observerErr := errors.New("durable observer failed")
	for _, tc := range []struct {
		name, mode                  string
		wantErr                     error
		wantStarted, wantDispatched int
	}{
		{name: "start-failure", wantErr: observerErr, wantStarted: 1},
		{name: "dispatch-failure", wantErr: observerErr, wantStarted: 1, wantDispatched: 1},
		{name: "cancel-before-start", wantErr: context.Canceled},
		{name: "cancel-before-dispatch", wantErr: context.Canceled, wantStarted: 1},
		{name: "cancel-before-release", wantErr: context.Canceled, wantStarted: 1, wantDispatched: 1},
		{name: "timeout-before-dispatch", wantErr: context.DeadlineExceeded, wantStarted: 1},
		{name: "timeout-before-release", wantErr: context.DeadlineExceeded, wantStarted: 1, wantDispatched: 1},
		{name: "blocked-child", mode: "blocked", wantErr: observerErr, wantStarted: 1, wantDispatched: 1},
		{name: "early-exit", mode: "exit", wantStarted: 1, wantDispatched: 1},
		{name: "success", wantStarted: 1, wantDispatched: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			capture, marker := filepath.Join(dir, "stdin"), filepath.Join(dir, "effect")
			client, err := New(Options{
				Args:        []string{"-test.run=^TestDispatchInputHelper$"},
				Environment: []string{dispatchCaptureEnv + "=" + capture, dispatchEffectEnv + "=" + marker, dispatchModeEnv + "=" + tc.mode},
				WaitDelay:   time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			timeout := 15 * time.Second
			if errors.Is(tc.wantErr, context.DeadlineExceeded) {
				timeout = 3 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			if tc.name == "cancel-before-start" {
				cancel()
			}
			started, dispatched, responded := 0, 0, 0
			request := protocol.ApplyRequest{PlanID: "plan-1", TargetID: "target-a", OperationID: "op-a", IdempotencyKey: "key", Attempt: 1, ProviderPayload: []byte("{}")}
			result, err := client.applyWithOptions(ctx, helperExecutable(t), request, callOptions{
				onStarted: func() error {
					started++
					if err := waitForEmptyDispatchCapture(ctx, capture); err != nil {
						return err
					}
					switch tc.name {
					case "start-failure":
						return observerErr
					case "cancel-before-dispatch":
						cancel()
					case "timeout-before-dispatch":
						<-ctx.Done()
					}
					return nil
				},
				onDispatched: func() error {
					dispatched++
					if err := waitForEmptyDispatchCapture(ctx, capture); err != nil {
						return err
					}
					switch tc.name {
					case "dispatch-failure", "blocked-child":
						return observerErr
					case "cancel-before-release":
						cancel()
					case "timeout-before-release":
						<-ctx.Done()
					}
					return nil
				},
				onResponse: func() error { responded++; return nil },
			})
			if started != tc.wantStarted || dispatched != tc.wantDispatched {
				t.Fatalf("callbacks: started=%d dispatched=%d err=%v", started, dispatched, err)
			}
			data, readErr := os.ReadFile(capture)
			if tc.name == "success" {
				if err != nil || result.Result.State != protocol.ResultPublished || responded != 1 {
					t.Fatalf("result=%+v responses=%d err=%v", result, responded, err)
				}
				if readErr != nil {
					t.Fatal(readErr)
				}
				received, decodeErr := protocol.NewCodec(protocol.DefaultMaxMessageBytes).DecodeRequest(bytes.NewReader(data))
				if decodeErr != nil || received.Operation != protocol.OperationApply || received.RequestID != "req-1" {
					t.Fatalf("received=%+v err=%v", received, decodeErr)
				}
				var payload protocol.ApplyRequest
				if err := protocol.DecodePayload(received.Payload, &payload); err != nil || payload.IdempotencyKey != request.IdempotencyKey || payload.OperationID != request.OperationID {
					t.Fatalf("payload=%+v err=%v", payload, err)
				}
				if effects, err := os.ReadFile(marker); err != nil || string(effects) != "apply\n" {
					t.Fatalf("effects=%q err=%v", effects, err)
				}
				return
			}
			if tc.name == "early-exit" {
				var processErr *ProcessError
				if !errors.As(err, &processErr) {
					t.Fatalf("expected process failure, got %v", err)
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want=%v", err, tc.wantErr)
			}
			if responded != 0 {
				t.Fatalf("unexpected response callback: %d", responded)
			}
			if tc.name == "cancel-before-start" {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("provider started after cancellation: %v", readErr)
				}
			} else if readErr != nil || len(data) != 0 {
				t.Fatalf("aborted provider received %d bytes: %v", len(data), readErr)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("aborted provider performed an effect: %v", err)
			}
		})
	}
}

type dispatchFaultDriver struct {
	*Driver
	writer *journal.Writer
}
type dispatchFaultObserver struct {
	executor.ExecutionObserver
	writer *journal.Writer
}

func (d dispatchFaultDriver) Apply(ctx context.Context, request executor.Request) (executor.Result, error) {
	request.Observer = dispatchFaultObserver{ExecutionObserver: request.Observer, writer: d.writer}
	return d.Driver.Apply(ctx, request)
}

func (o dispatchFaultObserver) SideEffectDispatched() error {
	if err := o.writer.Close(); err != nil {
		return err
	}
	return o.ExecutionObserver.SideEffectDispatched()
}

func TestExecutorAbortsApplyOnDispatchJournalFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "effect")
	plan := executorPlan(t, helperConfig{Mode: "process_crash_once", MarkerPath: marker})
	run, _ := domain.NewRunID("run-dispatch-fault")
	path := filepath.Join(t.TempDir(), "run.journal")
	writer, err := journal.OpenWriter(path, run)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	driver := dispatchFaultDriver{Driver: helperDriver(t, "normal"), writer: writer}
	engine, err := executor.New(driver, executor.Options{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(context.Background(), plan, run, writer); !errors.Is(err, journal.ErrWriterClosed) {
		t.Fatalf("expected journal failure, got %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("apply escaped failed journal: %v", err)
	}
	reopened, err := journal.OpenWriter(path, run)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	events := reopened.Events()
	state, err := journal.Reduce(plan, events)
	if err != nil {
		t.Fatalf("journal cannot replay: %v", err)
	}
	op, ok := state.Operation("publish")
	if !ok || op.State != domain.StateRunning || op.Attempt != 1 || !op.ProviderStarted || op.ReconcileRequired {
		t.Fatalf("unexpected replay state: %+v", op)
	}
	for _, event := range events {
		switch event.Type {
		case journal.EventSideEffectDispatched, journal.EventProviderResponseReceived, journal.EventOperationPublished:
			t.Fatalf("unexpected event after journal failure: %s", event.Type)
		}
	}
}
