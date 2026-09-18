package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aalsanie/distroplane/internal/domain"
)

var (
	ErrNotFound    = errors.New("credential reference is not configured")
	ErrUnavailable = errors.New("credential is unavailable")
	ErrReleased    = errors.New("credential material is released")
)

type Resolver interface {
	Resolve(context.Context, domain.CredentialRef) (*Material, error)
}

type Material struct {
	mu       sync.Mutex
	value    []byte
	released bool
}

func newMaterial(value []byte) (*Material, error) {
	if len(value) == 0 {
		return nil, ErrUnavailable
	}
	return &Material{value: append([]byte(nil), value...)}, nil
}

func (m *Material) Bytes() ([]byte, error) {
	if m == nil {
		return nil, ErrReleased
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.released {
		return nil, ErrReleased
	}
	return append([]byte(nil), m.value...), nil
}

func (m *Material) Release() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.released {
		return
	}
	for i := range m.value {
		m.value[i] = 0
	}
	m.value = nil
	m.released = true
}

func (*Material) String() string { return "[REDACTED]" }

func (*Material) GoString() string { return "credentials.Material{[REDACTED]}" }

func (*Material) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("credential material cannot be serialized")
}

type StaticResolver struct {
	values map[domain.CredentialRef][]byte
}

func NewStaticResolver(values map[domain.CredentialRef][]byte) (*StaticResolver, error) {
	copied := make(map[domain.CredentialRef][]byte, len(values))
	for ref, value := range values {
		if !ref.Valid() {
			return nil, fmt.Errorf("credential reference is invalid")
		}
		if len(value) == 0 {
			return nil, fmt.Errorf("%w: %q", ErrUnavailable, ref)
		}
		copied[ref] = append([]byte(nil), value...)
	}
	return &StaticResolver{values: copied}, nil
}

func (r *StaticResolver) Resolve(ctx context.Context, ref domain.CredentialRef) (*Material, error) {
	if err := validateResolve(ctx, ref); err != nil {
		return nil, err
	}
	if r == nil || r.values == nil {
		return nil, fmt.Errorf("static credential resolver is not initialized")
	}
	value, ok := r.values[ref]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, ref)
	}
	return newMaterial(value)
}

type EnvironmentResolver struct {
	sources map[domain.CredentialRef]string
}

func NewEnvironmentResolver(sources map[domain.CredentialRef]string) (*EnvironmentResolver, error) {
	copied := make(map[domain.CredentialRef]string, len(sources))
	for ref, source := range sources {
		if !ref.Valid() {
			return nil, fmt.Errorf("credential reference is invalid")
		}
		if !validEnvironmentName(source) {
			return nil, fmt.Errorf("credential environment source is invalid")
		}
		copied[ref] = source
	}
	return &EnvironmentResolver{sources: copied}, nil
}

func (r *EnvironmentResolver) Resolve(ctx context.Context, ref domain.CredentialRef) (*Material, error) {
	if err := validateResolve(ctx, ref); err != nil {
		return nil, err
	}
	if r == nil || r.sources == nil {
		return nil, fmt.Errorf("environment credential resolver is not initialized")
	}
	source, ok := r.sources[ref]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, ref)
	}
	value, ok := os.LookupEnv(source)
	if !ok || value == "" {
		return nil, fmt.Errorf("%w: %q", ErrUnavailable, ref)
	}
	return newMaterial([]byte(value))
}

type Chain struct {
	resolvers []Resolver
}

func NewChain(resolvers ...Resolver) (*Chain, error) {
	if len(resolvers) == 0 {
		return nil, fmt.Errorf("credential resolver chain must not be empty")
	}
	copied := make([]Resolver, len(resolvers))
	for i, resolver := range resolvers {
		if resolver == nil {
			return nil, fmt.Errorf("credential resolver must not be nil")
		}
		copied[i] = resolver
	}
	return &Chain{resolvers: copied}, nil
}

func (c *Chain) Resolve(ctx context.Context, ref domain.CredentialRef) (*Material, error) {
	if err := validateResolve(ctx, ref); err != nil {
		return nil, err
	}
	if c == nil || len(c.resolvers) == 0 {
		return nil, fmt.Errorf("credential resolver chain is not initialized")
	}
	for _, resolver := range c.resolvers {
		material, err := resolver.Resolve(ctx, ref)
		if err == nil {
			return material, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrNotFound, ref)
}

type Requirement struct {
	Ref         domain.CredentialRef
	Environment string
}

func ParseRequirement(value domain.Requirement) (Requirement, error) {
	if value.Kind() != "credential" {
		return Requirement{}, fmt.Errorf("requirement is not a credential requirement")
	}
	ref, err := domain.NewCredentialRef(value.Name())
	if err != nil {
		return Requirement{}, err
	}
	var metadata struct {
		Environment string `json:"environment"`
	}
	if len(value.Metadata()) == 0 {
		return Requirement{}, fmt.Errorf("credential requirement %q is missing environment metadata", ref)
	}
	if err := json.Unmarshal(value.Metadata(), &metadata); err != nil {
		return Requirement{}, fmt.Errorf("credential requirement %q metadata is invalid", ref)
	}
	if !validEnvironmentName(metadata.Environment) {
		return Requirement{}, fmt.Errorf("credential requirement %q environment is invalid", ref)
	}
	return Requirement{Ref: ref, Environment: metadata.Environment}, nil
}

func validateResolve(ctx context.Context, ref domain.CredentialRef) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ref.Valid() {
		return fmt.Errorf("credential reference is invalid")
	}
	return nil
}

func validEnvironmentName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return !strings.ContainsRune(value, '\x00')
}
