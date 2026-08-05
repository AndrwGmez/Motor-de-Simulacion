package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/flowverse/flowverse-api/internal/auth"
	"github.com/flowverse/flowverse-api/internal/domain"
	"github.com/flowverse/flowverse-api/internal/engine"
	"github.com/flowverse/flowverse-api/internal/store"
)

func TestSubscribeReplaysFiveHundredActiveEventsWithoutBlocking(t *testing.T) {
	const eventCount = 500
	events := make([]domain.Event, eventCount)
	for index := range events {
		events[index] = domain.Event{
			SchemaVersion: domain.SchemaVersion,
			Type:          "node.completed",
			RunID:         "run-replay",
			Sequence:      int64(index + 1),
			OccurredAt:    time.Unix(int64(index), 0).UTC(),
			LogicalTimeMS: int64(index),
			Payload:       map[string]any{"nodeId": "node"},
		}
	}
	repository := store.NewMemory()
	manager := NewManager(repository)
	current := &state{
		run:  domain.Run{ID: "run-replay", Status: "running", Events: events},
		wake: make(chan struct{}, 1), speed: 1, subs: map[chan domain.Event]struct{}{},
	}
	manager.active[current.run.ID] = current

	type subscription struct {
		events <-chan domain.Event
		cancel func()
		err    error
	}
	result := make(chan subscription, 1)
	go func() {
		eventStream, cancel, err := manager.Subscribe(current.run.ID, 0)
		result <- subscription{events: eventStream, cancel: cancel, err: err}
	}()

	var subscribed subscription
	select {
	case subscribed = <-result:
	case <-time.After(time.Second):
		t.Fatal("Subscribe blocked while replaying more than its live buffer")
	}
	if subscribed.err != nil {
		t.Fatal(subscribed.err)
	}
	for sequence := 1; sequence <= eventCount; sequence++ {
		select {
		case event, open := <-subscribed.events:
			if !open {
				t.Fatalf("stream closed after %d events", sequence-1)
			}
			if event.Sequence != int64(sequence) {
				t.Fatalf("sequence = %d, want %d", event.Sequence, sequence)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for replay event %d", sequence)
		}
	}
	subscribed.cancel()
	if _, open := <-subscribed.events; open {
		t.Fatal("cancel did not close the subscription")
	}
	manager.deactivate(current)
}

func TestPersistenceFailureInterruptsPlaybackAndPublishesOnlyDurableEvents(t *testing.T) {
	base := store.NewMemory()
	run := domain.Run{ID: "run-failure", Status: "created", CreatedAt: time.Now().UTC()}
	if _, _, err := base.CreateRun(context.Background(), run, store.RunIdempotency{}); err != nil {
		t.Fatal(err)
	}
	repository := &failingRepository{Repository: base, failAt: 3}
	manager := NewManager(repository)
	manager.tick = 50 * time.Millisecond
	result := engine.SimulationResult{
		RunID:  "run-failure",
		Status: "completed",
		Events: []domain.Event{
			{Type: "run.started", LogicalTimeMS: 0, Payload: map[string]any{}},
			{Type: "node.queued", LogicalTimeMS: 0, Payload: map[string]any{"nodeId": "start"}},
			{Type: "node.started", LogicalTimeMS: 0, Payload: map[string]any{"nodeId": "start"}},
			{Type: "run.completed", LogicalTimeMS: 0, Payload: map[string]any{}},
		},
		NodeRuns: []domain.NodeRun{},
		Output:   map[string]any{"ok": true},
	}
	if err := manager.Start(context.Background(), run, result); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool {
		persisted, err := base.RunByID(context.Background(), run.ID)
		return err == nil && len(persisted.Events) == 1
	})
	stream, cancel, err := manager.Subscribe(run.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	var event domain.Event
	select {
	case received, open := <-stream:
		if !open {
			t.Fatal("subscription closed without the durable interruption event")
		}
		event = received
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for interruption")
	}
	if event.Type != "run.interrupted" || event.Sequence != 2 {
		t.Fatalf("event = %+v, want durable run.interrupted sequence 2", event)
	}
	if _, open := <-stream; open {
		t.Fatal("subscription stayed open after interruption")
	}

	waitFor(t, func() bool {
		persisted, err := base.RunByID(context.Background(), run.ID)
		return err == nil && persisted.Status == "interrupted" && !manager.isActive(run.ID)
	})
	persisted, err := base.RunByID(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := eventTypes(persisted.Events); len(got) != 2 || got[0] != "run.started" || got[1] != "run.interrupted" {
		t.Fatalf("persisted events = %#v", got)
	}
	if persisted.CompletedAt == nil {
		t.Fatal("interrupted run has no completion timestamp")
	}
	if calls := repository.callCount(); calls != 4 {
		t.Fatalf("UpdateRun calls = %d, want queued + started + failure + interrupted", calls)
	}
	if err := manager.Pause(run.ID); !errors.Is(err, ErrRunNotActive) {
		t.Fatalf("Pause after interruption = %v, want ErrRunNotActive", err)
	}
}

func TestPersistentStorageFailureStopsWithoutPublishingUndurableEvents(t *testing.T) {
	base := store.NewMemory()
	run := domain.Run{ID: "run-outage", Status: "created", CreatedAt: time.Now().UTC()}
	if _, _, err := base.CreateRun(context.Background(), run, store.RunIdempotency{}); err != nil {
		t.Fatal(err)
	}
	repository := &failingRepository{Repository: base, failAt: 3, failFrom: true}
	manager := NewManager(repository)
	manager.tick = time.Millisecond
	result := engine.SimulationResult{
		RunID:  "run-outage",
		Status: "completed",
		Events: []domain.Event{
			{Type: "run.started", Payload: map[string]any{}},
			{Type: "node.queued", Payload: map[string]any{"nodeId": "start"}},
			{Type: "node.started", Payload: map[string]any{"nodeId": "start"}},
			{Type: "run.completed", Payload: map[string]any{}},
		},
	}
	if err := manager.Start(context.Background(), run, result); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !manager.isActive(run.ID) && repository.callCount() >= 4 })
	calls := repository.callCount()
	time.Sleep(20 * time.Millisecond)
	if repository.callCount() != calls {
		t.Fatalf("playback continued writing after interruption: %d -> %d calls", calls, repository.callCount())
	}
	persisted, err := base.RunByID(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := eventTypes(persisted.Events); len(got) != 1 || got[0] != "run.started" {
		t.Fatalf("undurable events became visible: %#v", got)
	}
}

func TestLimitEventFinishesRunAsFailed(t *testing.T) {
	base := store.NewMemory()
	run := domain.Run{ID: "run-limit", Status: "created", CreatedAt: time.Now().UTC()}
	if _, _, err := base.CreateRun(context.Background(), run, store.RunIdempotency{}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(base)
	manager.tick = time.Millisecond
	result := engine.SimulationResult{
		RunID:  "run-limit",
		Status: "failed",
		Error:  "maximum visits exceeded",
		Events: []domain.Event{
			{Type: "run.started", Payload: map[string]any{}},
			{Type: "run.limit_exceeded", Payload: map[string]any{"code": "run.max_visits_per_node"}},
		},
	}
	if err := manager.Start(context.Background(), run, result); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		persisted, err := base.RunByID(context.Background(), run.ID)
		return err == nil && persisted.Status == "failed" && !manager.isActive(run.ID)
	})
	persisted, _ := base.RunByID(context.Background(), run.ID)
	if persisted.CompletedAt == nil || persisted.Error != result.Error {
		t.Fatalf("terminal limit result not persisted: %+v", persisted)
	}
}

func TestControlRejectsAStateThatAlreadyReachedATerminalEvent(t *testing.T) {
	manager := NewManager(store.NewMemory())
	current := &state{
		run:      domain.Run{ID: "run-finished", Status: "completed"},
		finished: true,
		wake:     make(chan struct{}, 1),
		subs:     map[chan domain.Event]struct{}{},
	}
	manager.active[current.run.ID] = current

	if err := manager.Pause(current.run.ID); !errors.Is(err, ErrRunNotActive) {
		t.Fatalf("Pause after terminal event = %v, want ErrRunNotActive", err)
	}
	if manager.isActive(current.run.ID) {
		t.Fatal("terminal state remained active")
	}
}

func TestWebSocketTicketExpiresInThirtySecondsAndIsSingleUse(t *testing.T) {
	manager := NewManager(store.NewMemory())
	before := time.Now().UTC()
	raw, err := manager.NewTicket("run-ticket", "user-ticket")
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.RLock()
	stored := manager.tickets[auth.HashToken(raw)]
	manager.mu.RUnlock()
	ttl := stored.ExpiresAt.Sub(before)
	if ttl < 29*time.Second || ttl > 31*time.Second {
		t.Fatalf("ticket TTL = %s, want 30s", ttl)
	}
	if userID, err := manager.ConsumeTicket(raw, "run-ticket"); err != nil || userID != "user-ticket" {
		t.Fatalf("first consumption user=%q err=%v", userID, err)
	}
	if _, err := manager.ConsumeTicket(raw, "run-ticket"); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("second consumption err=%v, want ErrInvalidTicket", err)
	}
}

func TestStartupRecoveryDurablyInterruptsOnlyActiveRuns(t *testing.T) {
	ctx := context.Background()
	repository := store.NewMemory()
	active := domain.Run{
		ID: "run-active", Status: "running", CreatedAt: time.Now().UTC(),
		Events: []domain.Event{
			{SchemaVersion: domain.SchemaVersion, Type: "run.started", RunID: "run-active", Sequence: 1, LogicalTimeMS: 0, Payload: map[string]any{}},
			{SchemaVersion: domain.SchemaVersion, Type: "node.started", RunID: "run-active", Sequence: 7, LogicalTimeMS: 123, Payload: map[string]any{"nodeId": "process"}},
		},
	}
	terminal := domain.Run{ID: "run-completed", Status: "completed", CreatedAt: time.Now().UTC()}
	if _, _, err := repository.CreateRun(ctx, active, store.RunIdempotency{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.CreateRun(ctx, terminal, store.RunIdempotency{}); err != nil {
		t.Fatal(err)
	}
	recoveredAt := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	manager := NewManager(repository)
	count, err := manager.RecoverInterrupted(ctx, recoveredAt)
	if err != nil || count != 1 {
		t.Fatalf("recovery count=%d err=%v", count, err)
	}
	persisted, err := repository.RunByID(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "interrupted" || persisted.CompletedAt == nil || !persisted.CompletedAt.Equal(recoveredAt) {
		t.Fatalf("active run was not durably interrupted: %+v", persisted)
	}
	if len(persisted.Events) != 3 {
		t.Fatalf("events = %+v", persisted.Events)
	}
	event := persisted.Events[2]
	if event.Type != "run.interrupted" || event.Sequence != 8 || event.LogicalTimeMS != 123 ||
		event.Payload["code"] != "run.startup_recovery" {
		t.Fatalf("interruption event = %+v", event)
	}
	untouched, _ := repository.RunByID(ctx, terminal.ID)
	if untouched.Status != "completed" || len(untouched.Events) != 0 {
		t.Fatalf("terminal run changed: %+v", untouched)
	}
	secondCount, err := manager.RecoverInterrupted(ctx, recoveredAt.Add(time.Minute))
	if err != nil || secondCount != 0 {
		t.Fatalf("second recovery count=%d err=%v", secondCount, err)
	}
	again, _ := repository.RunByID(ctx, active.ID)
	if len(again.Events) != 3 {
		t.Fatalf("idempotent recovery appended another event: %+v", again.Events)
	}
}

type failingRepository struct {
	store.Repository
	mu       sync.Mutex
	calls    int
	failAt   int
	failFrom bool
}

func (f *failingRepository) UpdateRun(ctx context.Context, run domain.Run) error {
	if err := f.recordCall(); err != nil {
		return err
	}
	return f.Repository.UpdateRun(ctx, run)
}

func (f *failingRepository) AppendRunEvent(ctx context.Context, run domain.Run, event domain.Event) error {
	if err := f.recordCall(); err != nil {
		return err
	}
	return f.Repository.AppendRunEvent(ctx, run, event)
}

func (f *failingRepository) recordCall() error {
	f.mu.Lock()
	f.calls++
	call := f.calls
	shouldFail := call == f.failAt || f.failFrom && call >= f.failAt
	f.mu.Unlock()
	if shouldFail {
		return errors.New("simulated storage outage")
	}
	return nil
}

func (f *failingRepository) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (m *Manager) isActive(runID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, active := m.active[runID]
	return active
}

func eventTypes(events []domain.Event) []string {
	result := make([]string, len(events))
	for index, event := range events {
		result[index] = event.Type
	}
	return result
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}
