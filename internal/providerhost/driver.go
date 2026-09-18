package providerhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

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
	bindings map[domain.ProviderRef]string
}

func NewDriver(client *Client, bindings []Binding) (*Driver, error) {
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
	return &Driver{client: client, bindings: resolved}, nil
}

func (d *Driver) Apply(ctx context.Context, request executor.Request) (executor.Result, error) {
	if err := d.validateRequest(ctx, request); err != nil {
		return executor.Result{}, err
	}
	executable, err := d.resolve(request.Operation.Provider())
	if err != nil {
		return executor.Result{}, err
	}
	if err := d.verify(ctx, executable, request.Operation.Provider(), protocol.CapabilityApply); err != nil {
		return executor.Result{}, err
	}
	response, err := d.client.apply(ctx, executable, protocol.ApplyRequest{
		PlanID:          string(request.PlanID),
		TargetID:        string(request.Operation.TargetID()),
		OperationID:     string(request.Operation.ID()),
		IdempotencyKey:  executionKey(request),
		Attempt:         request.Attempt,
		ProviderPayload: append(json.RawMessage(nil), request.Operation.ProviderPayload().Bytes()...),
	})
	if err != nil {
		return executor.Result{}, mapError(err)
	}
	return mapResult(response.Result)
}

func (d *Driver) Reconcile(ctx context.Context, request executor.Request) (executor.Result, error) {
	if err := d.validateRequest(ctx, request); err != nil {
		return executor.Result{}, err
	}
	executable, err := d.resolve(request.Operation.Provider())
	if err != nil {
		return executor.Result{}, err
	}
	if err := d.verify(ctx, executable, request.Operation.Provider(), protocol.CapabilityReconcile); err != nil {
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
	response, err := d.client.reconcile(ctx, executable, protocolRequest)
	if err != nil {
		return executor.Result{}, mapError(err)
	}
	return mapResult(response.Result)
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
