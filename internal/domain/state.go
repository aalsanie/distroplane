package domain

import (
	"encoding/json"
	"fmt"
)

type NormalizedState string

const (
	StatePlanned         NormalizedState = "PLANNED"
	StateReady           NormalizedState = "READY"
	StateRunning         NormalizedState = "RUNNING"
	StateWaitingExternal NormalizedState = "WAITING_EXTERNAL"
	StatePublished       NormalizedState = "PUBLISHED"
	StateRejected        NormalizedState = "REJECTED"
	StateFailed          NormalizedState = "FAILED"
	StateCancelled       NormalizedState = "CANCELLED"
)

func (s NormalizedState) Valid() bool {
	switch s {
	case StatePlanned, StateReady, StateRunning, StateWaitingExternal, StatePublished, StateRejected, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

func (s NormalizedState) MarshalJSON() ([]byte, error) {
	if !s.Valid() {
		return nil, fmt.Errorf("invalid normalized state %q", s)
	}
	return json.Marshal(string(s))
}

func (s *NormalizedState) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	state := NormalizedState(value)
	if !state.Valid() {
		return fmt.Errorf("invalid normalized state %q", value)
	}
	*s = state
	return nil
}

type DistributionEvidence struct {
	targetID      TargetID
	state         NormalizedState
	providerState string
	data          JSONValue
}

func NewDistributionEvidence(targetID TargetID, state NormalizedState, providerState string, data JSONValue) (DistributionEvidence, error) {
	if !targetID.Valid() {
		return DistributionEvidence{}, fmt.Errorf("evidence target ID is invalid")
	}
	if !state.Valid() {
		return DistributionEvidence{}, fmt.Errorf("evidence state is invalid")
	}
	if err := validateText("provider state", providerState, true); err != nil {
		return DistributionEvidence{}, err
	}
	if !data.Valid() {
		return DistributionEvidence{}, fmt.Errorf("evidence data is invalid")
	}
	return DistributionEvidence{targetID: targetID, state: state, providerState: providerState, data: data}, nil
}

func (e DistributionEvidence) TargetID() TargetID     { return e.targetID }
func (e DistributionEvidence) State() NormalizedState { return e.state }
func (e DistributionEvidence) ProviderState() string  { return e.providerState }
func (e DistributionEvidence) Data() JSONValue        { return JSONValue{raw: e.data.Bytes()} }
