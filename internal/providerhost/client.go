package providerhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/protocol"
)

const defaultMaxStderrBytes = 64 << 10

var ErrOutputLimit = errors.New("provider stdout exceeds limit")

type Options struct {
	MaxMessageBytes int64
	MaxStderrBytes  int64
	Args            []string
	Environment     []string
	WaitDelay       time.Duration
}

type Client struct {
	codec       protocol.Codec
	maxStderr   int64
	args        []string
	environment []string
	waitDelay   time.Duration
	sequence    atomic.Uint64
}

type callOptions struct {
	environment  []string
	redactions   [][]byte
	onStarted    func() error
	onDispatched func() error
	onResponse   func() error
}

type gatedReader struct {
	reader io.Reader
	gate   <-chan struct{}
}

func (r *gatedReader) Read(p []byte) (int, error) {
	<-r.gate
	return r.reader.Read(p)
}

type ProcessError struct {
	Executable  string
	Err         error
	Diagnostics string
	Truncated   bool
}

func (e *ProcessError) Error() string {
	if e == nil {
		return ""
	}
	message := fmt.Sprintf("provider process %q failed: %v", e.Executable, e.Err)
	if e.Diagnostics != "" {
		message += ": " + e.Diagnostics
	}
	if e.Truncated {
		message += " [stderr truncated]"
	}
	return message
}

func (e *ProcessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type ProviderError struct {
	Value protocol.ProviderError
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Value.Code, e.Value.Message)
}

func New(options Options) (*Client, error) {
	if options.MaxMessageBytes < 0 {
		return nil, fmt.Errorf("max message bytes must not be negative")
	}
	if options.MaxStderrBytes < 0 {
		return nil, fmt.Errorf("max stderr bytes must not be negative")
	}
	if options.WaitDelay < 0 {
		return nil, fmt.Errorf("wait delay must not be negative")
	}
	maxMessage := options.MaxMessageBytes
	if maxMessage == 0 {
		maxMessage = protocol.DefaultMaxMessageBytes
	}
	maxStderr := options.MaxStderrBytes
	if maxStderr == 0 {
		maxStderr = defaultMaxStderrBytes
	}
	environment, err := normalizeEnvironment(options.Environment)
	if err != nil {
		return nil, err
	}
	return &Client{
		codec:       protocol.NewCodec(maxMessage),
		maxStderr:   maxStderr,
		args:        append([]string(nil), options.Args...),
		environment: environment,
		waitDelay:   options.WaitDelay,
	}, nil
}

func (c *Client) Describe(ctx context.Context, endpoint planner.Endpoint) (protocol.DescribeResponse, error) {
	var result protocol.DescribeResponse
	if err := c.call(ctx, endpoint.Executable, protocol.OperationDescribe, protocol.DescribeRequest{}, &result); err != nil {
		return protocol.DescribeResponse{}, err
	}
	return result, nil
}

func (c *Client) Plan(ctx context.Context, endpoint planner.Endpoint, request protocol.PlanRequest) (protocol.PlanResponse, error) {
	var result protocol.PlanResponse
	if err := c.call(ctx, endpoint.Executable, protocol.OperationPlan, request, &result); err != nil {
		return protocol.PlanResponse{}, err
	}
	return result, nil
}

func (c *Client) apply(ctx context.Context, executable string, request protocol.ApplyRequest) (protocol.ApplyResponse, error) {
	return c.applyWithOptions(ctx, executable, request, callOptions{})
}

func (c *Client) applyWithOptions(ctx context.Context, executable string, request protocol.ApplyRequest, options callOptions) (protocol.ApplyResponse, error) {
	var result protocol.ApplyResponse
	if err := c.callWithOptions(ctx, executable, protocol.OperationApply, request, &result, options); err != nil {
		return protocol.ApplyResponse{}, err
	}
	return result, nil
}

func (c *Client) reconcile(ctx context.Context, executable string, request protocol.ReconcileRequest) (protocol.ReconcileResponse, error) {
	return c.reconcileWithOptions(ctx, executable, request, callOptions{})
}

func (c *Client) reconcileWithOptions(ctx context.Context, executable string, request protocol.ReconcileRequest, options callOptions) (protocol.ReconcileResponse, error) {
	var result protocol.ReconcileResponse
	if err := c.callWithOptions(ctx, executable, protocol.OperationReconcile, request, &result, options); err != nil {
		return protocol.ReconcileResponse{}, err
	}
	return result, nil
}

func (c *Client) call(ctx context.Context, executable string, operation protocol.Operation, payload any, destination any) error {
	return c.callWithOptions(ctx, executable, operation, payload, destination, callOptions{})
}

func (c *Client) callWithOptions(ctx context.Context, executable string, operation protocol.Operation, payload any, destination any, options callOptions) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	if c == nil || c.codec.MaxMessageBytes <= 0 || c.maxStderr <= 0 {
		return fmt.Errorf("provider client is not initialized")
	}
	if !operation.Valid() {
		return fmt.Errorf("invalid provider operation %q", operation)
	}
	if destination == nil {
		return fmt.Errorf("response destination must not be nil")
	}
	executable, err := validateExecutable(executable)
	if err != nil {
		return err
	}
	rawPayload, err := protocol.EncodePayload(payload)
	if err != nil {
		return err
	}
	request := protocol.Request{
		ProtocolVersion: protocol.Version,
		RequestID:       fmt.Sprintf("req-%d", c.sequence.Add(1)),
		Operation:       operation,
		Payload:         rawPayload,
	}
	var input bytes.Buffer
	if err := c.codec.EncodeRequest(&input, request); err != nil {
		return err
	}

	stdout := &limitedWriter{limit: c.codec.MaxMessageBytes}
	stderr := &captureWriter{limit: c.maxStderr}
	command := exec.CommandContext(ctx, executable, c.args...)
	inputGate := make(chan struct{})
	inputReleased := false
	releaseInput := func() {
		if !inputReleased {
			close(inputGate)
			inputReleased = true
		}
	}
	command.Stdin = &gatedReader{reader: bytes.NewReader(input.Bytes()), gate: inputGate}
	command.Stdout = stdout
	command.Stderr = stderr
	environment := c.environment
	if options.environment != nil {
		environment = options.environment
	}
	command.Env = append([]string(nil), environment...)
	command.WaitDelay = c.waitDelay
	redactions := newRedactor(options.redactions)

	if err := command.Start(); err != nil {
		releaseInput()
		return &ProcessError{
			Executable: executable,
			Err:        err,
		}
	}
	cleanup := func() {
		releaseInput()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}
	if options.onStarted != nil {
		if err := options.onStarted(); err != nil {
			cleanup()
			return err
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		cleanup()
		return ctxErr
	}
	if options.onDispatched != nil {
		if err := options.onDispatched(); err != nil {
			cleanup()
			return err
		}
	}
	releaseInput()
	runErr := command.Wait()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if stdout.exceeded {
		return fmt.Errorf("%w: maximum is %d bytes", ErrOutputLimit, c.codec.MaxMessageBytes)
	}
	if runErr != nil {
		return &ProcessError{
			Executable:  executable,
			Err:         runErr,
			Diagnostics: redactions.text(stderr.String()),
			Truncated:   stderr.truncated,
		}
	}
	response, err := c.codec.DecodeResponse(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	if err := response.CheckCorrelation(request); err != nil {
		return err
	}
	if options.onResponse != nil {
		if err := options.onResponse(); err != nil {
			return err
		}
	}
	if response.Status == protocol.StatusError {
		value := *response.Error
		redactProviderError(&value, redactions)
		return &ProviderError{Value: value}
	}
	if err := decodeResponsePayload(response.Payload, destination); err != nil {
		return fmt.Errorf("decode provider payload: %w", err)
	}
	redactDestination(destination, redactions)
	return nil
}

func decodeResponsePayload(payload []byte, destination any) error {
	switch typed := destination.(type) {
	case *protocol.DescribeResponse:
		return protocol.DecodePayload(payload, typed)
	case *protocol.PlanResponse:
		return protocol.DecodePayload(payload, typed)
	case *protocol.ApplyResponse:
		return protocol.DecodePayload(payload, typed)
	case *protocol.ReconcileResponse:
		return protocol.DecodePayload(payload, typed)
	default:
		return fmt.Errorf("unsupported response destination %T", destination)
	}
}

func validateExecutable(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("provider executable must not be empty")
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("provider executable must be valid UTF-8")
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("provider executable must be an absolute path")
	}
	clean := filepath.Clean(value)
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("provider executable %q: %w", clean, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("provider executable %q is a directory", clean)
	}
	return clean, nil
}

func (c *Client) processEnvironment(extra []string) ([]string, error) {
	if c == nil {
		return nil, fmt.Errorf("provider client is not initialized")
	}
	return mergeEnvironment(c.environment, extra)
}

func mergeEnvironment(base, extra []string) ([]string, error) {
	environment := make(map[string]string, len(base)+len(extra))
	for _, value := range base {
		key, item, ok := strings.Cut(value, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid provider environment entry")
		}
		environment[key] = item
	}
	for _, value := range extra {
		key, item, ok := strings.Cut(value, "=")
		if !ok || key == "" || strings.ContainsRune(key, '\x00') || strings.ContainsRune(item, '\x00') {
			return nil, fmt.Errorf("invalid provider environment entry")
		}
		if _, exists := environment[key]; exists {
			return nil, fmt.Errorf("provider environment entry %q is already defined", key)
		}
		environment[key] = item
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result, nil
}

func normalizeEnvironment(values []string) ([]string, error) {
	environment := map[string]string{}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SYSTEMROOT", "WINDIR"} {
			if value := os.Getenv(key); value != "" {
				environment[key] = value
			}
		}
	}
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if !ok || key == "" || strings.ContainsRune(key, '\x00') || strings.ContainsRune(item, '\x00') {
			return nil, fmt.Errorf("invalid provider environment entry")
		}
		environment[key] = item
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result, nil
}

type limitedWriter struct {
	buffer   bytes.Buffer
	limit    int64
	exceeded bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.exceeded {
		return 0, ErrOutputLimit
	}
	remaining := w.limit - int64(w.buffer.Len())
	if int64(len(p)) <= remaining {
		return w.buffer.Write(p)
	}
	if remaining > 0 {
		_, _ = w.buffer.Write(p[:remaining])
	}
	w.exceeded = true
	return int(max(remaining, 0)), ErrOutputLimit
}

func (w *limitedWriter) Bytes() []byte {
	return w.buffer.Bytes()
}

type captureWriter struct {
	buffer    bytes.Buffer
	limit     int64
	truncated bool
}

func (w *captureWriter) Write(p []byte) (int, error) {
	original := len(p)
	remaining := w.limit - int64(w.buffer.Len())
	if remaining > 0 {
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
		_, _ = w.buffer.Write(p)
	}
	if int64(original) > remaining {
		w.truncated = true
	}
	return original, nil
}

func (w *captureWriter) String() string {
	return strings.TrimSpace(strings.ToValidUTF8(w.buffer.String(), "�"))
}

var _ planner.ProviderClient = (*Client)(nil)
var _ io.Writer = (*limitedWriter)(nil)
