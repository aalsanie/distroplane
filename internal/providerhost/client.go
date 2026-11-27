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
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/protocol"
)

const (
	defaultMaxStderrBytes  = 64 << 10
	defaultDescribeTimeout = 10 * time.Second
	defaultPlanTimeout     = 60 * time.Second
	defaultWaitDelay       = 2 * time.Second
)

var ErrOutputLimit = errors.New("provider stdout exceeds limit")

type Options struct {
	MaxMessageBytes int64
	MaxStderrBytes  int64
	Args            []string
	Environment     []string
	WaitDelay       time.Duration
	DescribeTimeout time.Duration
	PlanTimeout     time.Duration
}

type Client struct {
	codec           protocol.Codec
	maxStderr       int64
	args            []string
	environment     []string
	waitDelay       time.Duration
	describeTimeout time.Duration
	planTimeout     time.Duration
	sequence        atomic.Uint64
}

type callOptions struct {
	environment  []string
	redactions   [][]byte
	onStarted    func() error
	onDispatched func() error
	onResponse   func() error
}

// gatedReader makes request delivery a single, irreversible decision. Closing
// gate alone does not authorize any bytes: abort wakes the stdin copier at EOF.
type gatedReader struct {
	reader    io.Reader
	gate      chan struct{}
	once      sync.Once
	committed bool
}

func (r *gatedReader) Read(p []byte) (int, error) {
	<-r.gate
	if !r.committed {
		return 0, io.EOF
	}
	return r.reader.Read(p)
}

func (r *gatedReader) commit(check func() error) bool {
	r.once.Do(func() {
		r.committed = check() == nil
		close(r.gate)
	})
	return r.committed
}

func (r *gatedReader) abort() {
	r.once.Do(func() { close(r.gate) })
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
	if options.DescribeTimeout < 0 || options.PlanTimeout < 0 {
		return nil, fmt.Errorf("provider phase timeout must not be negative")
	}
	if options.DescribeTimeout == 0 {
		options.DescribeTimeout = defaultDescribeTimeout
	}
	if options.PlanTimeout == 0 {
		options.PlanTimeout = defaultPlanTimeout
	}
	if options.WaitDelay == 0 {
		options.WaitDelay = defaultWaitDelay
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
		codec:           protocol.NewCodec(maxMessage),
		maxStderr:       maxStderr,
		args:            append([]string(nil), options.Args...),
		environment:     environment,
		waitDelay:       options.WaitDelay,
		describeTimeout: options.DescribeTimeout,
		planTimeout:     options.PlanTimeout,
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

func (c *Client) phaseContext(parent context.Context, operation protocol.Operation) (context.Context, context.CancelFunc) {
	switch operation {
	case protocol.OperationDescribe:
		return context.WithTimeout(parent, c.describeTimeout)
	case protocol.OperationPlan:
		return context.WithTimeout(parent, c.planTimeout)
	default:
		if _, ok := parent.Deadline(); ok {
			return parent, func() {}
		}
		return context.WithTimeout(parent, domain.DefaultOperationTimeout)
	}
}

func invocationError(parent, phase context.Context) error {
	if err := parent.Err(); err != nil {
		return err
	}
	return phase.Err()
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
	parent := ctx
	ctx, cancel := c.phaseContext(parent, operation)
	defer cancel()
	check := func() error { return invocationError(parent, ctx) }
	if err := check(); err != nil {
		return err
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
	configureProcessTree(command)
	inputGate := &gatedReader{reader: bytes.NewReader(input.Bytes()), gate: make(chan struct{})}
	var terminateOnce sync.Once
	var terminateErr error
	treeReady := make(chan struct{})
	var terminateTree func() error
	command.Cancel = func() error {
		inputGate.abort()
		<-treeReady
		terminateOnce.Do(func() {
			terminateErr = terminateTree()
		})
		return terminateErr
	}
	command.Stdin = inputGate
	command.Stdout = stdout
	command.Stderr = stderr
	environment := c.environment
	if options.environment != nil {
		environment = options.environment
	}
	command.Env = make([]string, len(environment))
	copy(command.Env, environment)
	command.WaitDelay = c.waitDelay
	redactions := newRedactor(options.redactions)

	if err := command.Start(); err != nil {
		close(treeReady)
		inputGate.abort()
		if ctxErr := check(); ctxErr != nil {
			return ctxErr
		}
		return &ProcessError{
			Executable: executable,
			Err:        err,
		}
	}
	var releaseTree func()
	var treeErr error
	terminateTree, releaseTree, treeErr = attachProcessTree(command.Process)
	close(treeReady)
	defer releaseTree()
	cleanup := func() {
		_ = command.Cancel()
		_ = command.Wait()
	}
	if treeErr != nil {
		cleanup()
		return &ProcessError{Executable: executable, Err: treeErr}
	}
	if ctxErr := check(); ctxErr != nil {
		cleanup()
		return ctxErr
	}
	if options.onStarted != nil {
		if err := options.onStarted(); err != nil {
			cleanup()
			return err
		}
	}
	if ctxErr := check(); ctxErr != nil {
		cleanup()
		return ctxErr
	}
	if options.onDispatched != nil {
		if err := options.onDispatched(); err != nil {
			cleanup()
			return err
		}
	}
	if !inputGate.commit(check) {
		cleanup()
		return check()
	}
	runErr := command.Wait()
	if runErr != nil || check() != nil {
		_ = command.Cancel()
	}
	if ctxErr := check(); ctxErr != nil {
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

type environmentValue struct {
	key   string
	value string
}

func canonicalEnvironmentKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

func parseEnvironmentEntry(value string) (environmentValue, string, error) {
	key, item, ok := strings.Cut(value, "=")
	if !ok || key == "" || strings.ContainsRune(key, '\x00') || strings.ContainsRune(item, '\x00') {
		return environmentValue{}, "", fmt.Errorf("invalid provider environment entry")
	}
	return environmentValue{key: key, value: item}, canonicalEnvironmentKey(key), nil
}

func environmentList(environment map[string]environmentValue) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		entry := environment[key]
		result = append(result, entry.key+"="+entry.value)
	}
	return result
}

func mergeEnvironment(base, extra []string) ([]string, error) {
	environment := make(map[string]environmentValue, len(base)+len(extra))
	for _, value := range base {
		entry, canonical, err := parseEnvironmentEntry(value)
		if err != nil {
			return nil, err
		}
		if prior, exists := environment[canonical]; exists {
			return nil, fmt.Errorf("provider environment entry %q duplicates %q", entry.key, prior.key)
		}
		environment[canonical] = entry
	}

	extraNames := make(map[string]string, len(extra))
	for _, value := range extra {
		entry, canonical, err := parseEnvironmentEntry(value)
		if err != nil {
			return nil, err
		}
		if prior, exists := extraNames[canonical]; exists {
			return nil, fmt.Errorf("provider environment entry %q duplicates %q", entry.key, prior)
		}
		if prior, exists := environment[canonical]; exists {
			return nil, fmt.Errorf("provider environment entry %q conflicts with %q", entry.key, prior.key)
		}
		extraNames[canonical] = entry.key
		environment[canonical] = entry
	}
	return environmentList(environment), nil
}

func normalizeEnvironment(values []string) ([]string, error) {
	environment := make(map[string]environmentValue)
	baseline := []string{"PATH", "TMPDIR", "TMP", "TEMP"}
	if runtime.GOOS == "windows" {
		baseline = append(baseline, "SYSTEMROOT", "WINDIR", "PATHEXT")
	}
	for _, key := range baseline {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			canonical := canonicalEnvironmentKey(key)
			if _, exists := environment[canonical]; !exists {
				environment[canonical] = environmentValue{key: key, value: value}
			}
		}
	}

	explicitNames := make(map[string]string, len(values))
	for _, value := range values {
		entry, canonical, err := parseEnvironmentEntry(value)
		if err != nil {
			return nil, err
		}
		if prior, exists := explicitNames[canonical]; exists {
			return nil, fmt.Errorf("provider environment entry %q duplicates %q", entry.key, prior)
		}
		explicitNames[canonical] = entry.key
		environment[canonical] = entry
	}
	return environmentList(environment), nil
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
