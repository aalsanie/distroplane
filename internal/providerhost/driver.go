package providerhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aalsanie/distroplane/internal/credentials"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/executor"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/protocol"
)

type Binding struct {
	Provider   domain.ProviderRef
	Executable string
}

type Driver struct {
	client   *Client
	resolver credentials.Resolver
	bindings map[domain.ProviderRef]string
}

type preparedInvocationKey struct{}

type preparedInvocation struct {
	provider    domain.ProviderRef
	operationID domain.OperationID
	attempt     uint32
	operation   executor.DriverOperation
	environment []string
	redactions  [][]byte
	materials   []*credentials.Material
	used        atomic.Bool
	releaseOnce sync.Once
}

func NewDriver(client *Client, bindings []Binding) (*Driver, error) {
	return newDriver(client, nil, bindings)
}

func NewDriverWithCredentials(client *Client, resolver credentials.Resolver, bindings []Binding) (*Driver, error) {
	if resolver == nil {
		return nil, fmt.Errorf("credential resolver must not be nil")
	}
	return newDriver(client, resolver, bindings)
}

func newDriver(client *Client, resolver credentials.Resolver, bindings []Binding) (*Driver, error) {
	if client == nil {
		return nil, fmt.Errorf("provider client must not be nil")
	}
	resolved := make(map[domain.ProviderRef]string, len(bindings))
	for _, binding := range bindings {
		if !binding.Provider.Valid() {
			return nil, fmt.Errorf("provider binding is invalid")
		}
		executable, err := validateExecutable(binding.Executable)
		if err != nil {
			return nil, err
		}
		if _, exists := resolved[binding.Provider]; exists {
			return nil, fmt.Errorf("duplicate provider binding %q@%q", binding.Provider.Name(), binding.Provider.Version())
		}
		resolved[binding.Provider] = executable
	}
	return &Driver{client: client, resolver: resolver, bindings: resolved}, nil
}

func (d *Driver) Prepare(ctx context.Context, request executor.Request, operation executor.DriverOperation) (executor.Preparation, error) {
	if err := d.validateRequest(ctx, request); err != nil {
		return executor.Preparation{}, err
	}
	capability, err := capabilityFor(operation)
	if err != nil {
		return executor.Preparation{}, err
	}
	executable, err := d.resolve(request.Operation.Provider())
	if err != nil {
		return executor.Preparation{}, err
	}
	if err := d.verify(ctx, executable, request.Operation.Provider(), capability); err != nil {
		return executor.Preparation{}, err
	}

	requirements, err := credentialRequirements(request.Requirements)
	if err != nil {
		return executor.Preparation{}, &executor.DriverError{Code: "CREDENTIAL_REQUIREMENT_INVALID", Message: err.Error()}
	}
	if len(requirements) != 0 && d.resolver == nil {
		return executor.Preparation{}, &executor.DriverError{Code: "CREDENTIAL_UNAVAILABLE", Message: "credential resolver is not configured"}
	}

	materials := make([]*credentials.Material, 0, len(requirements))
	redactions := make([][]byte, 0, len(requirements))
	environment := make([]string, 0, len(requirements))
	refs := make([]domain.CredentialRef, 0, len(requirements))
	seenRefs := make(map[domain.CredentialRef]struct{}, len(requirements))
	cleanup := func() {
		for _, value := range redactions {
			for i := range value {
				value[i] = 0
			}
		}
		for _, material := range materials {
			material.Release()
		}
	}

	for _, requirement := range requirements {
		material, resolveErr := d.resolver.Resolve(ctx, requirement.Ref)
		if resolveErr != nil {
			cleanup()
			if errors.Is(resolveErr, context.Canceled) || errors.Is(resolveErr, context.DeadlineExceeded) {
				return executor.Preparation{}, resolveErr
			}
			if errors.Is(resolveErr, credentials.ErrNotFound) || errors.Is(resolveErr, credentials.ErrUnavailable) {
				return executor.Preparation{}, &executor.DriverError{
					Code: "CREDENTIAL_UNAVAILABLE", Message: fmt.Sprintf("credential %q is unavailable", requirement.Ref),
				}
			}
			return executor.Preparation{}, &executor.DriverError{
				Code: "CREDENTIAL_RESOLUTION_FAILED", Message: fmt.Sprintf("credential %q could not be resolved", requirement.Ref),
			}
		}
		value, valueErr := material.Bytes()
		if valueErr != nil {
			material.Release()
			cleanup()
			return executor.Preparation{}, &executor.DriverError{
				Code: "CREDENTIAL_RESOLUTION_FAILED", Message: fmt.Sprintf("credential %q could not be resolved", requirement.Ref),
			}
		}
		materials = append(materials, material)
		redactions = append(redactions, value)
		environment = append(environment, requirement.Environment+"="+string(value))
		if _, exists := seenRefs[requirement.Ref]; !exists {
			seenRefs[requirement.Ref] = struct{}{}
			refs = append(refs, requirement.Ref)
		}
	}

	processEnvironment, err := d.client.processEnvironment(environment)
	if err != nil {
		cleanup()
		return executor.Preparation{}, &executor.DriverError{Code: "CREDENTIAL_ENVIRONMENT_INVALID", Message: err.Error()}
	}
	sort.Slice(refs, func(i, j int) bool { return string(refs[i]) < string(refs[j]) })
	invocation := &preparedInvocation{
		provider: request.Operation.Provider(), operationID: request.Operation.ID(), attempt: request.Attempt,
		operation: operation, environment: processEnvironment, redactions: redactions, materials: materials,
	}
	preparedContext := context.WithValue(ctx, preparedInvocationKey{}, invocation)
	return executor.Preparation{
		Context: preparedContext, CredentialRefs: append([]domain.CredentialRef(nil), refs...), Release: invocation.release,
	}, nil
}

func (d *Driver) Apply(ctx context.Context, request executor.Request) (executor.Result, error) {
	ctx, release, err := d.ensurePrepared(ctx, request, executor.DriverApply)
	if err != nil {
		return executor.Result{}, err
	}
	if release != nil {
		defer release()
	}
	options, err := consumePrepared(ctx, request, executor.DriverApply)
	if err != nil {
		return executor.Result{}, err
	}
	executable, err := d.resolve(request.Operation.Provider())
	if err != nil {
		return executor.Result{}, err
	}
	response, err := d.client.applyWithOptions(ctx, executable, protocol.ApplyRequest{
		PlanID:          string(request.PlanID),
		TargetID:        string(request.Operation.TargetID()),
		OperationID:     string(request.Operation.ID()),
		IdempotencyKey:  executionKey(request),
		Attempt:         request.Attempt,
		ProviderPayload: append(json.RawMessage(nil), request.Operation.ProviderPayload().Bytes()...),
	}, options)
	if err != nil {
		return executor.Result{}, mapError(err)
	}
	return mapResult(response.Result)
}

func (d *Driver) Reconcile(ctx context.Context, request executor.Request) (executor.Result, error) {
	ctx, release, err := d.ensurePrepared(ctx, request, executor.DriverReconcile)
	if err != nil {
		return executor.Result{}, err
	}
	if release != nil {
		defer release()
	}
	options, err := consumePrepared(ctx, request, executor.DriverReconcile)
	if err != nil {
		return executor.Result{}, err
	}
	executable, err := d.resolve(request.Operation.Provider())
	if err != nil {
		return executor.Result{}, err
	}
	protocolRequest := protocol.ReconcileRequest{
		PlanID:          string(request.PlanID),
		TargetID:        string(request.Operation.TargetID()),
		OperationID:     string(request.Operation.ID()),
		IdempotencyKey:  executionKey(request),
		Attempt:         request.Attempt,
		ProviderPayload: append(json.RawMessage(nil), request.Operation.ProviderPayload().Bytes()...),
		Previous:        mapPrevious(request.Previous),
	}
	response, err := d.client.reconcileWithOptions(ctx, executable, protocolRequest, options)
	if err != nil {
		return executor.Result{}, mapError(err)
	}
	return mapResult(response.Result)
}

func (d *Driver) ensurePrepared(ctx context.Context, request executor.Request, operation executor.DriverOperation) (context.Context, func(), error) {
	if preparedFromContext(ctx) != nil {
		return ctx, nil, nil
	}
	preparation, err := d.Prepare(ctx, request, operation)
	if err != nil {
		return ctx, nil, err
	}
	return preparation.Context, preparation.Release, nil
}

func consumePrepared(ctx context.Context, request executor.Request, operation executor.DriverOperation) (callOptions, error) {
	invocation := preparedFromContext(ctx)
	if invocation == nil {
		return callOptions{}, fmt.Errorf("provider invocation is not prepared")
	}
	if invocation.provider != request.Operation.Provider() || invocation.operationID != request.Operation.ID() || invocation.attempt != request.Attempt || invocation.operation != operation {
		return callOptions{}, fmt.Errorf("provider invocation preparation does not match request")
	}
	if !invocation.used.CompareAndSwap(false, true) {
		return callOptions{}, fmt.Errorf("provider invocation preparation has already been used")
	}
	return callOptions{environment: invocation.environment, redactions: invocation.redactions}, nil
}

func preparedFromContext(ctx context.Context) *preparedInvocation {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(preparedInvocationKey{}).(*preparedInvocation)
	return value
}

func (i *preparedInvocation) release() {
	if i == nil {
		return
	}
	i.releaseOnce.Do(func() {
		for _, value := range i.redactions {
			for j := range value {
				value[j] = 0
			}
		}
		for _, material := range i.materials {
			material.Release()
		}
		for j := range i.environment {
			i.environment[j] = ""
		}
		i.redactions = nil
		i.materials = nil
		i.environment = nil
	})
}

func capabilityFor(operation executor.DriverOperation) (protocol.Capability, error) {
	switch operation {
	case executor.DriverApply:
		return protocol.CapabilityApply, nil
	case executor.DriverReconcile:
		return protocol.CapabilityReconcile, nil
	default:
		return "", fmt.Errorf("provider driver operation is invalid")
	}
}

func credentialRequirements(values []domain.Requirement) ([]credentials.Requirement, error) {
	result := make([]credentials.Requirement, 0)
	seenEnvironment := make(map[string]domain.CredentialRef)
	seenRef := make(map[domain.CredentialRef]string)
	for _, value := range values {
		if value.Kind() != "credential" {
			continue
		}
		requirement, err := credentials.ParseRequirement(value)
		if err != nil {
			return nil, err
		}
		if prior, exists := seenEnvironment[requirement.Environment]; exists {
			return nil, fmt.Errorf("credential environment %q is requested by both %q and %q", requirement.Environment, prior, requirement.Ref)
		}
		if prior, exists := seenRef[requirement.Ref]; exists {
			return nil, fmt.Errorf("credential %q is requested for both %q and %q", requirement.Ref, prior, requirement.Environment)
		}
		seenEnvironment[requirement.Environment] = requirement.Ref
		seenRef[requirement.Ref] = requirement.Environment
		result = append(result, requirement)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Environment != result[j].Environment {
			return result[i].Environment < result[j].Environment
		}
		return string(result[i].Ref) < string(result[j].Ref)
	})
	return result, nil
}

func (d *Driver) validateRequest(ctx context.Context, request executor.Request) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	if d == nil || d.client == nil || d.bindings == nil {
		return fmt.Errorf("provider driver is not initialized")
	}
	if !request.PlanID.Valid() {
		return fmt.Errorf("plan ID is invalid")
	}
	if !request.RunID.Valid() {
		return fmt.Errorf("run ID is invalid")
	}
	if request.Attempt == 0 {
		return fmt.Errorf("attempt must be greater than zero")
	}
	if !request.Operation.ID().Valid() || !request.Operation.TargetID().Valid() || !request.Operation.Provider().Valid() || !request.Operation.ProviderPayload().Valid() {
		return fmt.Errorf("operation is invalid")
	}
	for _, requirement := range request.Requirements {
		if !requirement.Valid() {
			return fmt.Errorf("requirement is invalid")
		}
	}
	return nil
}

func (d *Driver) resolve(provider domain.ProviderRef) (string, error) {
	executable, ok := d.bindings[provider]
	if !ok {
		return "", fmt.Errorf("provider %q@%q is not bound", provider.Name(), provider.Version())
	}
	return executable, nil
}

func (d *Driver) verify(ctx context.Context, executable string, provider domain.ProviderRef, capability protocol.Capability) error {
	description, err := d.client.Describe(ctx, planner.Endpoint{Executable: executable})
	if err != nil {
		return mapError(err)
	}
	if description.Provider.Name != string(provider.Name()) || description.Provider.Version != string(provider.Version()) {
		return &executor.DriverError{
			Code:    "PROVIDER_IDENTITY_MISMATCH",
			Message: fmt.Sprintf("provider reported %q@%q, expected %q@%q", description.Provider.Name, description.Provider.Version, provider.Name(), provider.Version()),
		}
	}
	supportsVersion := false
	for _, version := range description.ProtocolVersions {
		if version == protocol.Version {
			supportsVersion = true
			break
		}
	}
	if !supportsVersion {
		return &executor.DriverError{Code: "PROVIDER_PROTOCOL_MISMATCH", Message: fmt.Sprintf("provider does not support protocol version %q", protocol.Version)}
	}
	for _, candidate := range description.Capabilities {
		if candidate == capability {
			return nil
		}
	}
	return &executor.DriverError{Code: "PROVIDER_CAPABILITY_MISMATCH", Message: fmt.Sprintf("provider does not support capability %q", capability)}
}

func executionKey(request executor.Request) string {
	if key := request.Operation.IdempotencyKey(); key != "" {
		return key
	}
	material := string(request.PlanID) + "\x00" + string(request.Operation.TargetID()) + "\x00" + string(request.Operation.ID())
	sum := sha256.Sum256([]byte(material))
	return "op-" + hex.EncodeToString(sum[:])
}

func mapPrevious(previous *executor.Previous) *protocol.DistributionResult {
	if previous == nil {
		return nil
	}
	state, ok := toProtocolState(previous.State)
	if !ok || len(previous.Evidence) == 0 || !json.Valid(previous.Evidence) {
		return nil
	}
	return &protocol.DistributionResult{
		State:         state,
		ProviderState: previous.ProviderState,
		Evidence:      append(json.RawMessage(nil), previous.Evidence...),
	}
}

func mapResult(result protocol.DistributionResult) (executor.Result, error) {
	state, ok := fromProtocolState(result.State)
	if !ok {
		return executor.Result{}, fmt.Errorf("unsupported provider result state %q", result.State)
	}
	return executor.Result{
		State:         state,
		ProviderState: result.ProviderState,
		Evidence:      append(json.RawMessage(nil), result.Evidence...),
	}, nil
}

func toProtocolState(state domain.NormalizedState) (protocol.ResultState, bool) {
	switch state {
	case domain.StateWaitingExternal:
		return protocol.ResultWaitingExternal, true
	case domain.StatePublished:
		return protocol.ResultPublished, true
	case domain.StateRejected:
		return protocol.ResultRejected, true
	default:
		return "", false
	}
}

func fromProtocolState(state protocol.ResultState) (domain.NormalizedState, bool) {
	switch state {
	case protocol.ResultWaitingExternal:
		return domain.StateWaitingExternal, true
	case protocol.ResultPublished:
		return domain.StatePublished, true
	case protocol.ResultRejected:
		return domain.StateRejected, true
	default:
		return "", false
	}
}

func mapError(err error) error {
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		return err
	}
	return &executor.DriverError{
		Code:      string(providerErr.Value.Code),
		Message:   providerErr.Value.Message,
		Retryable: providerErr.Value.Retryable,
		Ambiguous: providerErr.Value.Code == protocol.ErrorAmbiguousOutcome,
	}
}

var _ executor.Driver = (*Driver)(nil)
var _ executor.Preparer = (*Driver)(nil)
