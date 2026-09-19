package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
)

const defaultLeaseTTL = 30 * time.Second

var (
	ErrLeaseHeld    = errors.New("execution lease is held")
	ErrLeaseExpired = errors.New("execution lease expired")
)

type LeaseRequest struct {
	Key         string
	OperationID domain.OperationID
}

type LeaseState struct {
	ID          string
	Owner       string
	OperationID domain.OperationID
	AcquiredAt  time.Time
	ExpiresAt   time.Time
}

type Lease interface {
	State() LeaseState
	PreviousExpired() (LeaseState, bool)
	Renew(context.Context) (LeaseState, error)
	Release() error
}

type LeaseManager interface {
	Acquire(context.Context, LeaseRequest) (Lease, error)
}

type leaseIDSource func() (string, error)

type MemoryLeases struct {
	mu    sync.Mutex
	held  map[string]LeaseState
	owner string
	ttl   time.Duration
	clock func() time.Time
	newID leaseIDSource
}

func NewMemoryLeases() *MemoryLeases {
	return newMemoryLeases(
		fmt.Sprintf("local-%d", os.Getpid()),
		defaultLeaseTTL,
		time.Now,
		randomLeaseID,
	)
}

func newMemoryLeases(owner string, ttl time.Duration, clock func() time.Time, newID leaseIDSource) *MemoryLeases {
	return &MemoryLeases{
		held:  make(map[string]LeaseState),
		owner: owner,
		ttl:   ttl,
		clock: clock,
		newID: newID,
	}
}

func randomLeaseID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate lease ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func (m *MemoryLeases) Acquire(ctx context.Context, request LeaseRequest) (Lease, error) {
	if ctx == nil {
		return nil, fmt.Errorf("lease context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.Key == "" {
		return nil, fmt.Errorf("lease key must not be empty")
	}
	if !request.OperationID.Valid() {
		return nil, fmt.Errorf("lease operation ID is invalid")
	}
	if err := m.validate(); err != nil {
		return nil, err
	}

	now := m.clock().UTC()
	if now.IsZero() {
		return nil, fmt.Errorf("lease clock returned zero time")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held == nil {
		m.held = make(map[string]LeaseState)
	}

	var expired *LeaseState
	if current, exists := m.held[request.Key]; exists {
		if current.ExpiresAt.After(now) {
			return nil, ErrLeaseHeld
		}
		copy := current
		expired = &copy
		delete(m.held, request.Key)
	}

	id, err := m.newID()
	if err != nil {
		return nil, err
	}
	if err := validateText("lease ID", id, false); err != nil {
		return nil, err
	}
	state := LeaseState{
		ID:          id,
		Owner:       m.owner,
		OperationID: request.OperationID,
		AcquiredAt:  now,
		ExpiresAt:   now.Add(m.ttl),
	}
	m.held[request.Key] = state
	return &memoryLease{manager: m, key: request.Key, state: state, previousExpired: expired}, nil
}

func (m *MemoryLeases) validate() error {
	if m == nil {
		return fmt.Errorf("lease manager is not initialized")
	}
	if err := validateText("lease owner", m.owner, false); err != nil {
		return err
	}
	if m.ttl <= 0 {
		return fmt.Errorf("lease TTL must be greater than zero")
	}
	if m.clock == nil {
		return fmt.Errorf("lease clock must not be nil")
	}
	if m.newID == nil {
		return fmt.Errorf("lease ID source must not be nil")
	}
	return nil
}

type memoryLease struct {
	mu              sync.Mutex
	manager         *MemoryLeases
	key             string
	state           LeaseState
	previousExpired *LeaseState
	released        bool
}

func (l *memoryLease) State() LeaseState {
	if l == nil {
		return LeaseState{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

func (l *memoryLease) PreviousExpired() (LeaseState, bool) {
	if l == nil {
		return LeaseState{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.previousExpired == nil {
		return LeaseState{}, false
	}
	return *l.previousExpired, true
}

func (l *memoryLease) Renew(ctx context.Context) (LeaseState, error) {
	if ctx == nil {
		return LeaseState{}, fmt.Errorf("lease context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return LeaseState{}, err
	}
	if l == nil {
		return LeaseState{}, fmt.Errorf("lease is not initialized")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released || l.manager == nil || l.key == "" {
		return l.state, ErrLeaseExpired
	}
	if err := l.manager.validate(); err != nil {
		return l.state, err
	}

	now := l.manager.clock().UTC()
	l.manager.mu.Lock()
	defer l.manager.mu.Unlock()
	current, exists := l.manager.held[l.key]
	if !exists || current.ID != l.state.ID || !current.ExpiresAt.After(now) {
		if exists && current.ID == l.state.ID {
			delete(l.manager.held, l.key)
		}
		return l.state, ErrLeaseExpired
	}

	expiresAt := now.Add(l.manager.ttl)
	if !expiresAt.After(current.ExpiresAt) {
		expiresAt = current.ExpiresAt.Add(l.manager.ttl)
	}
	current.ExpiresAt = expiresAt
	l.manager.held[l.key] = current
	l.state = current
	return current, nil
}

func (l *memoryLease) Release() error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	if l.manager == nil || l.key == "" {
		return fmt.Errorf("lease is invalid")
	}
	if err := l.manager.validate(); err != nil {
		return err
	}

	now := l.manager.clock().UTC()
	l.manager.mu.Lock()
	defer l.manager.mu.Unlock()
	current, exists := l.manager.held[l.key]
	if !exists || current.ID != l.state.ID {
		l.released = true
		return ErrLeaseExpired
	}
	if !current.ExpiresAt.After(now) {
		delete(l.manager.held, l.key)
		l.released = true
		return ErrLeaseExpired
	}
	delete(l.manager.held, l.key)
	l.released = true
	return nil
}
