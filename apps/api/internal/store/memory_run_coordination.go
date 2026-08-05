package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
)

const MaxRunEventListLimit = 1000

func (m *Memory) AcquireRunLease(
	_ context.Context,
	request RunLeaseRequest,
	limits RunCapacityLimits,
) (RunLease, error) {
	if err := validateLeaseRequest(request); err != nil {
		return RunLease{}, err
	}
	if limits.Global < 0 || limits.Actor < 0 || limits.Project < 0 {
		return RunLease{}, fmt.Errorf("run capacity limits cannot be negative")
	}
	request.Now = request.Now.UTC()

	m.mu.Lock()
	defer m.mu.Unlock()
	run, exists := m.runs[request.RunID]
	if !exists {
		return RunLease{}, ErrNotFound
	}
	if !activeRunStatus(run.Status) {
		return RunLease{}, ErrConflict
	}
	if current, exists := m.runLeases[request.RunID]; exists && activeLease(current, request.Now) {
		if current.InstanceID != request.InstanceID {
			return RunLease{}, &RunLeaseHeldError{RetryAfter: normalizedRetryAfter(current.ExpiresAt.Sub(request.Now))}
		}
		current.HeartbeatAt = request.Now
		current.ExpiresAt = request.Now.Add(request.TTL)
		m.runLeases[request.RunID] = current
		return cloneRunLease(current), nil
	}

	if limit, retry := m.runQuotaLocked(request.RunID, "", "", limits.Global, request.Now); limit {
		return RunLease{}, &RunQuotaError{Scope: RunQuotaGlobal, RetryAfter: retry}
	}
	if request.ActorID != "" {
		if limit, retry := m.runQuotaLocked(request.RunID, request.ActorID, "", limits.Actor, request.Now); limit {
			return RunLease{}, &RunQuotaError{Scope: RunQuotaActor, RetryAfter: retry}
		}
	}
	if run.ProjectID != "" {
		if limit, retry := m.runQuotaLocked(request.RunID, "", run.ProjectID, limits.Project, request.Now); limit {
			return RunLease{}, &RunQuotaError{Scope: RunQuotaProject, RetryAfter: retry}
		}
	}

	lease := RunLease{
		RunID: request.RunID, InstanceID: request.InstanceID,
		ActorID: request.ActorID, ProjectID: run.ProjectID,
		AcquiredAt: request.Now, HeartbeatAt: request.Now, ExpiresAt: request.Now.Add(request.TTL),
	}
	m.runLeases[request.RunID] = lease
	return cloneRunLease(lease), nil
}

func (m *Memory) runQuotaLocked(
	excludedRunID, actorID, projectID string,
	limit int,
	now time.Time,
) (bool, time.Duration) {
	if limit == 0 {
		return false, 0
	}
	count := 0
	var earliest time.Time
	for runID, lease := range m.runLeases {
		if runID == excludedRunID || !activeLease(lease, now) {
			continue
		}
		run, exists := m.runs[runID]
		if !exists || !activeRunStatus(run.Status) {
			continue
		}
		if actorID != "" && lease.ActorID != actorID || projectID != "" && lease.ProjectID != projectID {
			continue
		}
		count++
		if earliest.IsZero() || lease.ExpiresAt.Before(earliest) {
			earliest = lease.ExpiresAt
		}
	}
	if count < limit {
		return false, 0
	}
	return true, normalizedRetryAfter(earliest.Sub(now))
}

func (m *Memory) HeartbeatRunLease(_ context.Context, request RunLeaseRequest) (RunLease, error) {
	if err := validateLeaseRequest(request); err != nil {
		return RunLease{}, err
	}
	request.Now = request.Now.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, exists := m.runLeases[request.RunID]
	run, runExists := m.runs[request.RunID]
	if !exists || !runExists || lease.InstanceID != request.InstanceID || !activeLease(lease, request.Now) || !activeRunStatus(run.Status) {
		return RunLease{}, ErrRunLeaseLost
	}
	lease.HeartbeatAt = request.Now
	lease.ExpiresAt = request.Now.Add(request.TTL)
	m.runLeases[request.RunID] = lease
	return cloneRunLease(lease), nil
}

func (m *Memory) ReleaseRunLease(_ context.Context, runID, instanceID string, releasedAt time.Time) error {
	if runID == "" || instanceID == "" || releasedAt.IsZero() {
		return fmt.Errorf("run id, instance id, and release time are required")
	}
	releasedAt = releasedAt.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, exists := m.runLeases[runID]
	if !exists {
		return ErrNotFound
	}
	if lease.InstanceID != instanceID {
		return ErrRunLeaseLost
	}
	if lease.ReleasedAt != nil {
		return nil
	}
	lease.ReleasedAt = &releasedAt
	lease.ExpiresAt = releasedAt
	m.runLeases[runID] = lease
	return nil
}

func (m *Memory) InterruptExpiredRunLeases(_ context.Context, occurredAt time.Time) (int, error) {
	if occurredAt.IsZero() {
		occurredAt = m.now().UTC()
	} else {
		occurredAt = occurredAt.UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	interrupted := 0
	for runID, lease := range m.runLeases {
		if lease.ReleasedAt != nil || lease.ExpiresAt.After(occurredAt) {
			continue
		}
		run, exists := m.runs[runID]
		if !exists || !activeRunStatus(run.Status) {
			continue
		}
		m.runs[runID] = clone(interruptRun(run, occurredAt))
		lease.ReleasedAt = &occurredAt
		lease.ExpiresAt = occurredAt
		m.runLeases[runID] = lease
		interrupted++
	}
	for runID, run := range m.runs {
		if !activeRunStatus(run.Status) {
			continue
		}
		m.runs[runID] = clone(interruptRun(run, occurredAt))
		interrupted++
	}
	return interrupted, nil
}

func (m *Memory) AppendRunEventWithLease(
	_ context.Context,
	run domain.Run,
	event domain.Event,
	instanceID string,
	now time.Time,
) error {
	if instanceID == "" || now.IsZero() {
		return fmt.Errorf("instance id and append time are required")
	}
	now = now.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, exists := m.runLeases[run.ID]
	if !exists || lease.InstanceID != instanceID || !activeLease(lease, now) {
		return ErrRunLeaseLost
	}
	return m.appendRunEventLocked(run, event)
}

func (m *Memory) UpdateRunWithLease(
	_ context.Context,
	run domain.Run,
	instanceID string,
	now time.Time,
) error {
	if instanceID == "" || now.IsZero() {
		return fmt.Errorf("instance id and update time are required")
	}
	now = now.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, exists := m.runLeases[run.ID]
	if !exists || lease.InstanceID != instanceID || !activeLease(lease, now) {
		return ErrRunLeaseLost
	}
	if _, exists := m.runs[run.ID]; !exists {
		return ErrNotFound
	}
	m.runs[run.ID] = clone(run)
	return nil
}

func (m *Memory) ListRunEvents(_ context.Context, runID string, afterSequence int64, limit int) ([]domain.Event, error) {
	if afterSequence < 0 || limit < 1 || limit > MaxRunEventListLimit {
		return nil, fmt.Errorf("invalid run event cursor or limit")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, exists := m.runs[runID]
	if !exists {
		return nil, ErrNotFound
	}
	events := make([]domain.Event, 0, min(limit, len(run.Events)))
	for _, event := range run.Events {
		if event.Sequence > afterSequence {
			events = append(events, clone(event))
		}
	}
	sort.Slice(events, func(left, right int) bool { return events[left].Sequence < events[right].Sequence })
	if len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

var _ RunCoordinator = (*Memory)(nil)
