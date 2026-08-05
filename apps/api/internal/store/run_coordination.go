package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
)

var (
	ErrRunLeaseHeld     = errors.New("run lease is held by another instance")
	ErrRunLeaseLost     = errors.New("run lease is no longer owned by this instance")
	ErrRunQuotaExceeded = errors.New("run capacity quota exceeded")
)

type RunQuotaScope string

const (
	RunQuotaGlobal  RunQuotaScope = "global"
	RunQuotaActor   RunQuotaScope = "actor"
	RunQuotaProject RunQuotaScope = "project"
)

type RunCapacityLimits struct {
	Global  int
	Actor   int
	Project int
}

type RunLeaseRequest struct {
	RunID      string
	InstanceID string
	ActorID    string
	Now        time.Time
	TTL        time.Duration
}

type RunLease struct {
	RunID       string
	InstanceID  string
	ActorID     string
	ProjectID   string
	AcquiredAt  time.Time
	HeartbeatAt time.Time
	ExpiresAt   time.Time
	ReleasedAt  *time.Time
}

type RunQuotaError struct {
	Scope      RunQuotaScope
	RetryAfter time.Duration
}

func (e *RunQuotaError) Error() string {
	return fmt.Sprintf("%s: %s quota", ErrRunQuotaExceeded, e.Scope)
}

func (e *RunQuotaError) Unwrap() error { return ErrRunQuotaExceeded }

type RunLeaseHeldError struct {
	RetryAfter time.Duration
}

func (e *RunLeaseHeldError) Error() string { return ErrRunLeaseHeld.Error() }

func (e *RunLeaseHeldError) Unwrap() error { return ErrRunLeaseHeld }

func normalizedRetryAfter(value time.Duration) time.Duration {
	if value < time.Second {
		return time.Second
	}
	return value
}

func validateLeaseRequest(request RunLeaseRequest) error {
	if request.RunID == "" {
		return fmt.Errorf("run id is required")
	}
	if request.InstanceID == "" || len(request.InstanceID) > 128 {
		return fmt.Errorf("instance id must contain between 1 and 128 characters")
	}
	if request.Now.IsZero() {
		return fmt.Errorf("lease time is required")
	}
	if request.TTL < time.Second {
		return fmt.Errorf("lease TTL must be at least one second")
	}
	return nil
}

func activeLease(lease RunLease, now time.Time) bool {
	return lease.ReleasedAt == nil && lease.ExpiresAt.After(now)
}

func cloneRunLease(lease RunLease) RunLease {
	if lease.ReleasedAt != nil {
		releasedAt := *lease.ReleasedAt
		lease.ReleasedAt = &releasedAt
	}
	return lease
}

// RunCoordinator is part of Repository so wrappers cannot accidentally bypass
// fencing. Appending an event without proving lease ownership is reserved for
// migrations, recovery, and compatibility tests.
type RunCoordinator interface {
	AcquireRunLease(context.Context, RunLeaseRequest, RunCapacityLimits) (RunLease, error)
	HeartbeatRunLease(context.Context, RunLeaseRequest) (RunLease, error)
	ReleaseRunLease(context.Context, string, string, time.Time) error
	InterruptExpiredRunLeases(context.Context, time.Time) (int, error)
	UpdateRunWithLease(context.Context, domain.Run, string, time.Time) error
	AppendRunEventWithLease(context.Context, domain.Run, domain.Event, string, time.Time) error
	ListRunEvents(context.Context, string, int64, int) ([]domain.Event, error)
}
