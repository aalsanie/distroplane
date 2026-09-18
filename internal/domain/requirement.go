package domain

import (
	"encoding/json"
	"fmt"
)

type Requirement struct {
	kind     string
	name     string
	metadata []byte
}

func NewRequirement(kind, name string, metadata []byte) (Requirement, error) {
	if err := validateText("requirement kind", kind, false); err != nil {
		return Requirement{}, err
	}
	if err := validateText("requirement name", name, false); err != nil {
		return Requirement{}, err
	}
	if len(metadata) != 0 && !json.Valid(metadata) {
		return Requirement{}, fmt.Errorf("requirement metadata must be valid JSON")
	}
	return Requirement{kind: kind, name: name, metadata: append([]byte(nil), metadata...)}, nil
}

func (r Requirement) Kind() string { return r.kind }

func (r Requirement) Name() string { return r.name }

func (r Requirement) Metadata() []byte { return append([]byte(nil), r.metadata...) }

func (r Requirement) Valid() bool {
	_, err := NewRequirement(r.kind, r.name, r.metadata)
	return err == nil
}

func cloneRequirements(values []Requirement) []Requirement {
	if len(values) == 0 {
		return nil
	}
	result := make([]Requirement, len(values))
	for i, value := range values {
		result[i] = Requirement{kind: value.kind, name: value.name, metadata: append([]byte(nil), value.metadata...)}
	}
	return result
}
