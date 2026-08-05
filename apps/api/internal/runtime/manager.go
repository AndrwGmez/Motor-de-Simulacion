package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/engine"
	"github.com/flowverse/flowverse-api/internal/store"
	"github.com/flowverse/flowverse-api/internal/telemetry"
)

var (
	ErrRunNotActive  = errors.New("run is not active")
	ErrInvalidTicket = errors.New("invalid or expired websocket ticket")
)

type state struct {
	mu          sync.Mutex
	context     context.Context
	span        trace.Span
	run         domain.Run
	planned     []domain.Event
	index       int
	paused      bool
	step        bool
	cancelled   bool
	interrupted bool
	finished    bool
	speed       float64
	wake        chan struct{}
	subs        map[chan domain.Event]struct{}
	lease       *leaseGuard
	pausedAt    *time.Time
}

type ticket struct {
	RunID     string
	UserID    string
	ExpiresAt time.Time
}

type Manager struct {
	repository   store.Repository
	mu           sync.RWMutex
	active       map[string]*state
	tickets      map[string]ticket
	tick         time.Duration
	reservations map[string]*leaseGuard
	config       Config
}

func NewManager(repository store.Repository) *Manager {
	return NewManagerWithConfig(repository, Config{})
}

func NewManagerWithConfig(repository store.Repository, config Config) *Manager {
	config = normalizeConfig(config)
	return &Manager{
		repository: repository, active: map[string]*state{}, tickets: map[string]ticket{},
		tick: 20 * time.Millisecond, reservations: map[string]*leaseGuard{}, config: config,
	}
}

// RecoverInterrupted durably closes executions that could not survive a
// process restart. It must run once during bootstrap before the API accepts
// traffic; the repository operation is idempotent.
func (m *Manager) RecoverInterrupted(ctx context.Context, occurredAt time.Time) (int, error) {
	return m.repository.InterruptExpiredRunLeases(ctx, occurredAt)
}

func (m *Manager) Start(ctx context.Context, run domain.Run, result engine.SimulationResult) error {
	run.Status = "queued"
	run.Events = []domain.Event{}
	run.NodeRuns = []domain.NodeRun{}
	if err := m.repository.UpdateRun(ctx, run); err != nil {
		return err
	}
	playbackContext, playbackSpan := telemetry.Tracer().Start(
		context.WithoutCancel(ctx),
		"flowverse.run.playback",
		trace.WithAttributes(
			attribute.String("flowverse.run.id", run.ID),
			attribute.String("flowverse.flow.id", run.FlowID),
			attribute.String("flowverse.flow.version_id", run.VersionID),
			attribute.Int("flowverse.run.planned_events", len(result.Events)),
		),
	)
	target := "draft"
	if run.VersionID != "" {
		target = "version"
	}
	telemetry.RunStarted(playbackContext, target)
	current := &state{
		context: playbackContext, span: playbackSpan,
		run: run, planned: result.Events, speed: 1, wake: make(chan struct{}, 1),
		subs: map[chan domain.Event]struct{}{},
	}
	m.mu.Lock()
	m.active[run.ID] = current
	m.mu.Unlock()
	go m.play(current, result)
	return nil
}

func (m *Manager) play(current *state, result engine.SimulationResult) {
	defer m.finishTelemetry(current)
	for {
		current.mu.Lock()
		if current.cancelled || current.interrupted {
			m.closeSubscribersLocked(current)
			current.mu.Unlock()
			m.deactivate(current)
			return
		}
		if current.index >= len(current.planned) {
			current.finished = true
			m.closeSubscribersLocked(current)
			current.mu.Unlock()
			m.deactivate(current)
			return
		}
		if current.paused && !current.step {
			current.mu.Unlock()
			<-current.wake
			continue
		}
		event := current.planned[current.index]
		if event.Type == "run.started" {
			now := time.Now().UTC()
			current.run.StartedAt = &now
			current.run.Status = "running"
		}
		if event.Type == "run.completed" {
			current.run.Status = "completed"
		}
		if event.Type == "run.failed" {
			current.run.Status = "failed"
		}
		if event.Type == "run.limit_exceeded" {
			current.run.Status = "failed"
		}
		if event.Type == "run.cancelled" {
			current.run.Status = "cancelled"
		}
		if event.Type == "run.interrupted" {
			current.run.Status = "interrupted"
		}
		terminal := isTerminalEvent(event.Type)
		if terminal {
			current.run.NodeRuns = result.NodeRuns
			current.run.Output = result.Output
			current.run.Error = result.Error
			completed := time.Now().UTC()
			current.run.CompletedAt = &completed
		}
		if err := m.appendLocked(current, event); err != nil {
			current.mu.Unlock()
			m.deactivate(current)
			return
		}
		current.index++
		if current.step && (event.Type == "node.completed" || event.Type == "node.failed" || event.Type == "node.skipped") {
			current.step = false
			current.paused = true
			current.run.Status = "paused"
			if err := m.appendLocked(current, domain.Event{
				Type: "run.paused", LogicalTimeMS: event.LogicalTimeMS,
				Payload: map[string]any{"reason": "step_completed"},
			}); err != nil {
				current.mu.Unlock()
				m.deactivate(current)
				return
			}
		}
		speed := current.speed
		if terminal {
			current.finished = true
			m.closeSubscribersLocked(current)
			current.mu.Unlock()
			m.deactivate(current)
			return
		}
		current.mu.Unlock()
		delay := time.Duration(float64(m.tick) / speed)
		if delay < time.Millisecond {
			delay = time.Millisecond
		}
		time.Sleep(delay)
	}
}

func (m *Manager) appendLocked(current *state, event domain.Event) error {
	event.SchemaVersion = domain.SchemaVersion
	event.RunID = current.run.ID
	event.Sequence = int64(len(current.run.Events) + 1)
	event.OccurredAt = time.Now().UTC()
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	// Durability precedes publication. A failed write stops publication and
	// leaves the last durable sequence available for replay.
	if err := m.repository.AppendRunEvent(current.context, current.run, event); err != nil {
		m.interruptLocked(current)
		return err
	}
	current.run.Events = append(current.run.Events, event)
	telemetry.RunEvent(current.context, event.Type)
	m.publishLocked(current, event)
	return nil
}

// interruptLocked stops playback immediately. If persistence has recovered, it
// records a durable run.interrupted event; otherwise only the last successfully
// persisted sequence remains observable and no undurable event is published.
func (m *Manager) interruptLocked(current *state) {
	current.interrupted = true
	now := time.Now().UTC()
	event := domain.Event{
		SchemaVersion: domain.SchemaVersion,
		Type:          "run.interrupted",
		RunID:         current.run.ID,
		Sequence:      int64(len(current.run.Events) + 1),
		OccurredAt:    now,
		LogicalTimeMS: lastLogical(current.run.Events),
		Payload:       map[string]any{"code": "persistence.failed"},
	}
	current.run.Status = "interrupted"
	current.run.Error = "run interrupted because an event could not be persisted"
	current.run.CompletedAt = &now
	if err := m.repository.AppendRunEvent(current.context, current.run, event); err == nil {
		current.run.Events = append(current.run.Events, event)
		telemetry.RunEvent(current.context, event.Type)
		m.publishLocked(current, event)
	}
	m.closeSubscribersLocked(current)
}

func (m *Manager) finishTelemetry(current *state) {
	current.mu.Lock()
	status, runError := current.run.Status, current.run.Error
	span := current.span
	ctx := current.context
	current.mu.Unlock()
	telemetry.RunFinished(ctx, status)
	if span == nil {
		return
	}
	span.SetAttributes(attribute.String("flowverse.run.status", status))
	if runError != "" {
		span.RecordError(errors.New(runError))
		span.SetStatus(codes.Error, runError)
	} else if status == "completed" {
		span.SetStatus(codes.Ok, "completed")
	}
	span.End()
}

func (m *Manager) publishLocked(current *state, event domain.Event) {
	for subscriber := range current.subs {
		select {
		case subscriber <- event:
		default:
			delete(current.subs, subscriber)
			close(subscriber)
		}
	}
}

func (m *Manager) closeSubscribersLocked(current *state) {
	for subscriber := range current.subs {
		delete(current.subs, subscriber)
		close(subscriber)
	}
}

func (m *Manager) deactivate(current *state) {
	m.mu.Lock()
	if active, ok := m.active[current.run.ID]; ok && active == current {
		delete(m.active, current.run.ID)
	}
	m.mu.Unlock()
}

func (m *Manager) Pause(runID string) error {
	return m.control(runID, func(current *state) error {
		if !current.paused {
			current.paused = true
			current.run.Status = "paused"
			return m.appendLocked(current, domain.Event{
				Type: "run.paused", LogicalTimeMS: lastLogical(current.run.Events), Payload: map[string]any{},
			})
		}
		return nil
	})
}

func (m *Manager) Resume(runID string) error {
	return m.control(runID, func(current *state) error {
		if current.paused {
			current.paused = false
			current.step = false
			current.run.Status = "running"
			if err := m.appendLocked(current, domain.Event{
				Type: "run.resumed", LogicalTimeMS: lastLogical(current.run.Events), Payload: map[string]any{},
			}); err != nil {
				return err
			}
			notify(current.wake)
		}
		return nil
	})
}

func (m *Manager) Step(runID string) error {
	return m.control(runID, func(current *state) error {
		if !current.paused {
			return errors.New("run must be paused before stepping")
		}
		current.step = true
		current.run.Status = "running"
		notify(current.wake)
		return nil
	})
}

func (m *Manager) SetSpeed(runID string, speed float64) error {
	if speed < 0.1 || speed > 16 {
		return errors.New("speed must be between 0.1 and 16")
	}
	return m.control(runID, func(current *state) error {
		current.speed = speed
		return nil
	})
}

func (m *Manager) Cancel(runID string) error {
	return m.control(runID, func(current *state) error {
		current.cancelled = true
		current.run.Status = "cancelled"
		now := time.Now().UTC()
		current.run.CompletedAt = &now
		if err := m.appendLocked(current, domain.Event{
			Type: "run.cancelled", LogicalTimeMS: lastLogical(current.run.Events), Payload: map[string]any{},
		}); err != nil {
			return err
		}
		m.closeSubscribersLocked(current)
		notify(current.wake)
		return nil
	})
}

func (m *Manager) control(runID string, action func(*state) error) error {
	m.mu.RLock()
	current, ok := m.active[runID]
	m.mu.RUnlock()
	if !ok {
		return ErrRunNotActive
	}
	current.mu.Lock()
	if current.finished || current.cancelled || current.interrupted {
		current.mu.Unlock()
		m.deactivate(current)
		return ErrRunNotActive
	}
	err := action(current)
	inactive := current.cancelled || current.interrupted
	current.mu.Unlock()
	if inactive {
		m.deactivate(current)
	}
	return err
}

func (m *Manager) Subscribe(runID string, afterSequence int64) (<-chan domain.Event, func(), error) {
	if afterSequence < 0 {
		afterSequence = 0
	}
	m.mu.RLock()
	current, active := m.active[runID]
	m.mu.RUnlock()
	if !active {
		run, err := m.repository.RunByID(context.Background(), runID)
		if err != nil {
			return nil, nil, err
		}
		replay := eventsAfter(run.Events, afterSequence)
		channel := make(chan domain.Event, len(replay))
		for _, event := range replay {
			channel <- event
		}
		close(channel)
		return channel, func() {}, nil
	}

	current.mu.Lock()
	replay := eventsAfter(current.run.Events, afterSequence)
	// Reserve live capacity in addition to the complete replay. Filling this
	// buffer is non-blocking even when replay contains thousands of events.
	channel := make(chan domain.Event, len(replay)+128)
	for _, event := range replay {
		channel <- event
	}
	if current.cancelled || current.interrupted || current.finished {
		close(channel)
	} else {
		current.subs[channel] = struct{}{}
	}
	current.mu.Unlock()
	cancel := func() {
		current.mu.Lock()
		if _, exists := current.subs[channel]; exists {
			delete(current.subs, channel)
			close(channel)
		}
		current.mu.Unlock()
	}
	return channel, cancel, nil
}

func eventsAfter(events []domain.Event, afterSequence int64) []domain.Event {
	result := make([]domain.Event, 0, len(events))
	for _, event := range events {
		if event.Sequence > afterSequence {
			result = append(result, event)
		}
	}
	return result
}

func isTerminalEvent(eventType string) bool {
	switch eventType {
	case "run.completed", "run.failed", "run.limit_exceeded", "run.cancelled", "run.interrupted":
		return true
	default:
		return false
	}
}

func (m *Manager) NewTicket(runID, userID string) (string, error) {
	raw, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tickets[auth.HashToken(raw)] = ticket{RunID: runID, UserID: userID, ExpiresAt: time.Now().UTC().Add(30 * time.Second)}
	return raw, nil
}

func (m *Manager) ConsumeTicket(raw, runID string) (string, error) {
	hash := auth.HashToken(raw)
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.tickets[hash]
	delete(m.tickets, hash)
	if !ok || value.RunID != runID || !value.ExpiresAt.After(time.Now().UTC()) {
		return "", ErrInvalidTicket
	}
	return value.UserID, nil
}

func lastLogical(events []domain.Event) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].LogicalTimeMS
}

func notify(channel chan struct{}) {
	select {
	case channel <- struct{}{}:
	default:
	}
}

func NewRunID() string { return uuid.NewString() }
