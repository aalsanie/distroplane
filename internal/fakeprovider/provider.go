package fakeprovider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aalsanie/distroplane/internal/protocol"
)

type Provider struct {
	Name string
}

type config struct {
	Mode           string `json:"mode,omitempty"`
	ReconcileState string `json:"reconcileState,omitempty"`
	Credential     string `json:"credential,omitempty"`
}

func (p Provider) Describe(context.Context, protocol.DescribeRequest) (protocol.DescribeResponse, *protocol.ProviderError) {
	name := p.Name
	if name == "" {
		name = "fake"
	}
	return protocol.DescribeResponse{
		Provider:         protocol.ProviderIdentity{Name: name, Version: "1.0.0"},
		ProtocolVersions: []string{protocol.Version},
		Capabilities:     []protocol.Capability{protocol.CapabilityPlan, protocol.CapabilityApply, protocol.CapabilityReconcile},
	}, nil
}

func (Provider) Plan(_ context.Context, request protocol.PlanRequest) (protocol.PlanResponse, *protocol.ProviderError) {
	cfg, err := parseConfig(request.Target.Configuration)
	if err != nil {
		providerErr := protocol.NewProviderError(protocol.ErrorConfiguration, err.Error(), false)
		return protocol.PlanResponse{}, &providerErr
	}
	if cfg.Mode == "plan_error" {
		providerErr := protocol.NewProviderError(protocol.ErrorConfiguration, "fake plan failure", false)
		return protocol.PlanResponse{}, &providerErr
	}
	if cfg.Mode == "noop" {
		return protocol.PlanResponse{Operations: []protocol.PlannedOperation{}}, nil
	}
	payload, _ := json.Marshal(cfg)
	response := protocol.PlanResponse{
		Operations: []protocol.PlannedOperation{{
			ID:              "publish",
			Kind:            "publish",
			SideEffecting:   true,
			TimeoutMillis:   30000,
			ProviderPayload: payload,
		}},
	}
	if cfg.Credential != "" {
		response.Requirements = []protocol.Requirement{{
			Kind: "credential", Name: cfg.Credential, Metadata: json.RawMessage(`{"environment":"DISTROPLANE_FAKE_CREDENTIAL"}`),
		}}
	}
	return response, nil
}

func (Provider) Apply(_ context.Context, request protocol.ApplyRequest) (protocol.ApplyResponse, *protocol.ProviderError) {
	cfg, err := parseConfig(request.ProviderPayload)
	if err != nil {
		providerErr := protocol.NewProviderError(protocol.ErrorConfiguration, err.Error(), false)
		return protocol.ApplyResponse{}, &providerErr
	}
	switch cfg.Mode {
	case "", "published", "noop":
		return applyResult(protocol.ResultPublished, "published"), nil
	case "waiting_external":
		return applyResult(protocol.ResultWaitingExternal, "pending"), nil
	case "rejected":
		return applyResult(protocol.ResultRejected, "rejected"), nil
	case "transient_error":
		return protocol.ApplyResponse{}, providerError(protocol.ErrorTransientExternal, "fake transient failure", true)
	case "permanent_error":
		return protocol.ApplyResponse{}, providerError(protocol.ErrorPermanentExternal, "fake permanent failure", false)
	case "authentication_error":
		return protocol.ApplyResponse{}, providerError(protocol.ErrorAuthentication, "fake authentication failure", false)
	case "authorization_error":
		return protocol.ApplyResponse{}, providerError(protocol.ErrorAuthorization, "fake authorization failure", false)
	case "timeout":
		return protocol.ApplyResponse{}, providerError(protocol.ErrorTimeout, "fake timeout", true)
	case "cancelled":
		return protocol.ApplyResponse{}, providerError(protocol.ErrorCancelled, "fake cancellation", false)
	case "ambiguous":
		return protocol.ApplyResponse{}, providerError(protocol.ErrorAmbiguousOutcome, "fake ambiguous outcome", false)
	case "internal_error":
		return protocol.ApplyResponse{}, providerError(protocol.ErrorProviderInternal, "fake internal failure", false)
	default:
		return protocol.ApplyResponse{}, providerError(protocol.ErrorConfiguration, fmt.Sprintf("unknown fake mode %q", cfg.Mode), false)
	}
}

func (Provider) Reconcile(_ context.Context, request protocol.ReconcileRequest) (protocol.ReconcileResponse, *protocol.ProviderError) {
	cfg, err := parseConfig(request.ProviderPayload)
	if err != nil {
		return protocol.ReconcileResponse{}, providerError(protocol.ErrorConfiguration, err.Error(), false)
	}
	state := cfg.ReconcileState
	if state == "" {
		if cfg.Mode == "ambiguous" {
			state = "published"
		} else {
			state = cfg.Mode
		}
	}
	switch state {
	case "", "published", "noop":
		result := applyResult(protocol.ResultPublished, "published").Result
		return protocol.ReconcileResponse{Result: result}, nil
	case "waiting_external":
		result := applyResult(protocol.ResultWaitingExternal, "pending").Result
		return protocol.ReconcileResponse{Result: result}, nil
	case "rejected":
		result := applyResult(protocol.ResultRejected, "rejected").Result
		return protocol.ReconcileResponse{Result: result}, nil
	case "transient_error":
		return protocol.ReconcileResponse{}, providerError(protocol.ErrorTransientExternal, "fake reconcile failure", true)
	default:
		return protocol.ReconcileResponse{}, providerError(protocol.ErrorConfiguration, fmt.Sprintf("unknown reconcile state %q", state), false)
	}
}

func parseConfig(raw json.RawMessage) (config, error) {
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return config{}, fmt.Errorf("invalid fake configuration: %w", err)
	}
	return cfg, nil
}

func applyResult(state protocol.ResultState, providerState string) protocol.ApplyResponse {
	return protocol.ApplyResponse{Result: protocol.DistributionResult{
		State:         state,
		ProviderState: providerState,
		Evidence:      json.RawMessage(`{"provider":"fake"}`),
	}}
}

func providerError(code protocol.ErrorCode, message string, retryable bool) *protocol.ProviderError {
	value := protocol.NewProviderError(code, message, retryable)
	return &value
}
