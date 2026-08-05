package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/store"
)

var (
	ErrPlannedEventLimit = errors.New("planned event limit exceeded")
	ErrRunWallTimeLimit  = errors.New("run wall-time limit exceeded")
	ErrRunPausedTimeout  = errors.New("run paused-time limit exceeded")
)

type Config struct {
	InstanceID        string
	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration
	GlobalRunLimit    int
	ActorRunLimit     int
	ProjectRunLimit   int
	MaxPlannedEvents  int
	MaxWallTime       time.Duration
	MaxPausedTime     time.Duration
	PollInterval      time.Duration
	Clock             func() time.Time
}

func normalizeConfig(config Config) Config {
	if config.InstanceID == "" {
		config.InstanceID = uuid.NewString()
	}
	if config.LeaseTTL <= 0 {
		config.LeaseTTL = 15 * time.Second
	}
	if config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.LeaseTTL {
		config.HeartbeatInterval = config.LeaseTTL / 3
	}
	if config.HeartbeatInterval < 10*time.Millisecond {
		config.HeartbeatInterval = 10 * time.Millisecond
	}
	if config.GlobalRunLimit <= 0 {
		config.GlobalRunLimit = 100
	}
	if config.ActorRunLimit <= 0 {
		config.ActorRunLimit = 5
	}
	if config.ProjectRunLimit <= 0 {
		config.ProjectRunLimit = 20
	}
	if config.MaxPlannedEvents <= 0 {
		config.MaxPlannedEvents = 100_000
	}
	if config.MaxWallTime <= 0 {
		config.MaxWallTime = 15 * time.Minute
	}
	if config.MaxPausedTime <= 0 {
		config.MaxPausedTime = 5 * time.Minute
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 100 * time.Millisecond
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return config
}

type PlannedEventLimitError struct {
	Actual  int
	Maximum int
}

func (e *PlannedEventLimitError) Error() string {
	return fmt.Sprintf("%s: planned %d events, maximum %d", ErrPlannedEventLimit, e.Actual, e.Maximum)
}

func (e *PlannedEventLimitError) Unwrap() error { return ErrPlannedEventLimit }

type leaseGuard struct {
	runID   string
	actorID string
	stop    chan struct{}
	lost    chan struct{}
	stopOne sync.Once
	lostOne sync.Once
}

func newLeaseGuard(runID, actorID string) *leaseGuard {
	return &leaseGuard{runID: runID, actorID: actorID, stop: make(chan struct{}), lost: make(chan struct{})}
}

func (guard *leaseGuard) markLost() {
	guard.lostOne.Do(func() { close(guard.lost) })
}

func (guard *leaseGuard) stopHeartbeat() {
	guard.stopOne.Do(func() { close(guard.stop) })
}

func (guard *leaseGuard) isLost() bool {
	select {
	case <-guard.lost:
		return true
	default:
		return false
	}
}

func (m *Manager) Reserve(ctx context.Context, run domain.Run, actorID string) error {
	request := store.RunLeaseRequest{
		RunID: run.ID, InstanceID: m.config.InstanceID, ActorID: actorID,
		Now: m.now(), TTL: m.config.LeaseTTL,
	}
	if _, err := m.repository.AcquireRunLease(ctx, request, store.RunCapacityLimits{
		Global: m.config.GlobalRunLimit, Actor: m.config.ActorRunLimit, Project: m.config.ProjectRunLimit,
	}); err != nil {
		return err
	}
	guard := newLeaseGuard(run.ID, actorID)
	m.mu.Lock()
	if _, active := m.active[run.ID]; active || m.reservations[run.ID] != nil {
		m.mu.Unlock()
		_ = m.repository.ReleaseRunLease(context.Background(), run.ID, m.config.InstanceID, m.now())
		return store.ErrConflict
	}
	m.reservations[run.ID] = guard
	m.mu.Unlock()
	go m.heartbeat(guard)
	return nil
}

func (m *Manager) heartbeat(guard *leaseGuard) {
	ticker := time.NewTicker(m.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-guard.stop:
			return
		case <-ticker.C:
			request := store.RunLeaseRequest{
				RunID: guard.runID, InstanceID: m.config.InstanceID, ActorID: guard.actorID,
				Now: m.now(), TTL: m.config.LeaseTTL,
			}
			ctx, cancel := context.WithTimeout(context.Background(), m.config.HeartbeatInterval)
			_, err := m.repository.HeartbeatRunLease(ctx, request)
			cancel()
			if err != nil {
				guard.markLost()
				return
			}
		}
	}
}

func (m *Manager) takeReservation(runID string) *leaseGuard {
	m.mu.Lock()
	defer m.mu.Unlock()
	guard := m.reservations[runID]
	delete(m.reservations, runID)
	return guard
}

func (m *Manager) Abandon(runID string) {
	guard := m.takeReservation(runID)
	if guard != nil {
		m.releaseGuard(guard)
	}
}

func (m *Manager) FailReserved(ctx context.Context, run domain.Run, message string) error {
	guard := m.takeReservation(run.ID)
	if guard == nil {
		return store.ErrRunLeaseLost
	}
	defer m.releaseGuard(guard)
	if guard.isLost() {
		return store.ErrRunLeaseLost
	}
	now := m.now()
	run.Status = "failed"
	run.Error = message
	run.CompletedAt = &now
	return m.repository.UpdateRunWithLease(ctx, run, m.config.InstanceID, now)
}

func (m *Manager) releaseGuard(guard *leaseGuard) {
	guard.stopHeartbeat()
	_ = m.repository.ReleaseRunLease(context.Background(), guard.runID, m.config.InstanceID, m.now())
}

func (m *Manager) now() time.Time { return m.config.Clock().UTC() }

