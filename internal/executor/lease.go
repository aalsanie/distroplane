package executor

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrLeaseHeld = errors.New("execution lease is held")

type Lease interface {
	Release() error
}

type LeaseManager interface {
	Acquire(context.Context, string) (Lease, error)
}

type MemoryLeases struct {
	mu   sync.Mutex
	held map[string]struct{}
}

func NewMemoryLeases() *MemoryLeases {
	return &MemoryLeases{held: make(map[string]struct{})}
}

func (m *MemoryLeases) Acquire(ctx context.Context, key string) (Lease, error) {
	if ctx == nil {
		return nil, fmt.Errorf("lease context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("lease key must not be empty")
	}
	if m == nil {
		return nil, fmt.Errorf("lease manager is not initialized")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held == nil {
		m.held = make(map[string]struct{})
	}
	if _, exists := m.held[key]; exists {
		return nil, ErrLeaseHeld
	}
	m.held[key] = struct{}{}
	return &memoryLease{manager: m, key: key}, nil
}

type memoryLease struct {
	mu       sync.Mutex
	manager  *MemoryLeases
	key      string
	released bool
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
	l.manager.mu.Lock()
	defer l.manager.mu.Unlock()
	if _, exists := l.manager.held[l.key]; !exists {
		return fmt.Errorf("lease %q is not held", l.key)
	}
	delete(l.manager.held, l.key)
	l.released = true
	return nil
}
