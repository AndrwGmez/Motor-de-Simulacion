package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/flowverse/flowverse-api/internal/domain"
)

func TestPostgresRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repository, err := OpenPostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user := domain.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@example.com",
		DisplayName: "PostgreSQL Test", PasswordHash: "test-only", CreatedAt: time.Now().UTC(),
	}
	if err := repository.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{
		ID: uuid.NewString(), Name: "Integration", OwnerID: user.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := repository.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteProject(context.Background(), project.ID)
	flow := domain.Flow{
		ID: uuid.NewString(), ProjectID: project.ID, Name: "Flow", DraftETag: `"one"`,
		Draft: domain.FlowDefinition{
			SchemaVersion: domain.SchemaVersion, Name: "Flow", Variables: []domain.VariableDefinition{},
			Layout: domain.Layout{Mode: "force"}, Nodes: []domain.Node{}, Edges: []domain.Edge{},
		},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := repository.CreateFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	flow.DraftETag = `"two"`
	if err := repository.UpdateFlow(ctx, flow, `"stale"`); err != ErrPrecondition {
		t.Fatalf("stale ETag err = %v", err)
	}
	if err := repository.UpdateFlow(ctx, flow, `"one"`); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.FlowByID(ctx, flow.ID)
	if err != nil || stored.DraftETag != `"two"` {
		t.Fatalf("stored flow = %+v, err %v", stored, err)
	}
	draftRun := domain.Run{
		ID: uuid.NewString(), ProjectID: project.ID, FlowID: flow.ID, VersionID: "",
		Status: "created", Definition: flow.Draft, DefinitionETag: flow.DraftETag, CreatedAt: time.Now().UTC(),
	}
	draftIdempotency := RunIdempotency{
		UserID: user.ID, TargetType: "flow_draft", TargetID: flow.ID,
		TargetRevision: flow.DraftETag, Key: "draft-" + uuid.NewString(), RequestHash: strings.Repeat("a", 64),
	}
	if _, created, err := repository.CreateRun(ctx, draftRun, draftIdempotency); err != nil || !created {
		t.Fatalf("create draft snapshot run: %v", err)
	}
	replayCandidate := draftRun
	replayCandidate.ID = uuid.NewString()
	replayed, created, err := repository.CreateRun(ctx, replayCandidate, draftIdempotency)
	if err != nil || created || replayed.ID != draftRun.ID {
		t.Fatalf("replayed run=%+v created=%v err=%v", replayed, created, err)
	}
	mismatched := draftIdempotency
	mismatched.RequestHash = strings.Repeat("b", 64)
	if _, _, err := repository.CreateRun(ctx, replayCandidate, mismatched); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("mismatched request err=%v", err)
	}

	otherTarget := draftIdempotency
	otherTarget.TargetRevision = `"three"`
	otherTargetRun := draftRun
	otherTargetRun.ID, otherTargetRun.DefinitionETag, otherTargetRun.CreatedAt = uuid.NewString(), otherTarget.TargetRevision, time.Now().UTC()
	if createdRun, created, err := repository.CreateRun(ctx, otherTargetRun, otherTarget); err != nil || !created || createdRun.ID != otherTargetRun.ID {
		t.Fatalf("other target run=%+v created=%v err=%v", createdRun, created, err)
	}

	otherUser := domain.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@example.com",
		DisplayName: "Other User", PasswordHash: "test-only", CreatedAt: time.Now().UTC(),
	}
	if err := repository.CreateUser(ctx, otherUser); err != nil {
		t.Fatal(err)
	}
	otherUserKey := draftIdempotency
	otherUserKey.UserID = otherUser.ID
	otherUserRun := draftRun
	otherUserRun.ID, otherUserRun.CreatedAt = uuid.NewString(), time.Now().UTC()
	if createdRun, created, err := repository.CreateRun(ctx, otherUserRun, otherUserKey); err != nil || !created || createdRun.ID != otherUserRun.ID {
		t.Fatalf("other user run=%+v created=%v err=%v", createdRun, created, err)
	}

	expiredKey := RunIdempotency{
		UserID: user.ID, TargetType: "flow_draft", TargetID: flow.ID,
		TargetRevision: `"expired"`, Key: "expired-" + uuid.NewString(), RequestHash: strings.Repeat("c", 64),
	}
	expiredRun := draftRun
	expiredRun.ID, expiredRun.CreatedAt = uuid.NewString(), time.Now().UTC()
	if _, created, err := repository.CreateRun(ctx, expiredRun, expiredKey); err != nil || !created {
		t.Fatalf("create expired claim: created=%v err=%v", created, err)
	}
	if _, err := repository.pool.Exec(ctx, `UPDATE run_idempotency_keys
		SET created_at=now()-interval '25 hours', expires_at=now()-interval '1 hour'
		WHERE run_id=$1`, expiredRun.ID); err != nil {
		t.Fatal(err)
	}
	replacement := expiredRun
	replacement.ID, replacement.CreatedAt = uuid.NewString(), time.Now().UTC()
	expiredKey.RequestHash = strings.Repeat("d", 64)
	replaced, created, err := repository.CreateRun(ctx, replacement, expiredKey)
	if err != nil || !created || replaced.ID != replacement.ID {
		t.Fatalf("replace expired claim run=%+v created=%v err=%v", replaced, created, err)
	}
	var retentionSeconds int64
	if err := repository.pool.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM expires_at-created_at)::bigint FROM run_idempotency_keys WHERE run_id=$1`,
		replacement.ID).Scan(&retentionSeconds); err != nil {
		t.Fatal(err)
	}
	if time.Duration(retentionSeconds)*time.Second < RunIdempotencyRetention {
		t.Fatalf("idempotency retention=%s, want at least %s",
			time.Duration(retentionSeconds)*time.Second, RunIdempotencyRetention)
	}

	concurrentKey := RunIdempotency{
		UserID: user.ID, TargetType: "flow_draft", TargetID: flow.ID,
		TargetRevision: `"concurrent"`, Key: "concurrent-" + uuid.NewString(), RequestHash: strings.Repeat("e", 64),
	}
	const workers = 8
	start := make(chan struct{})
	var wait sync.WaitGroup
	var mutex sync.Mutex
	createdCount := 0
	returnedIDs := map[string]int{}
	errs := make([]error, 0)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			candidate := draftRun
			candidate.ID, candidate.CreatedAt = uuid.NewString(), time.Now().UTC()
			result, created, createErr := repository.CreateRun(ctx, candidate, concurrentKey)
			mutex.Lock()
			defer mutex.Unlock()
			if createErr != nil {
				errs = append(errs, createErr)
				return
			}
			if created {
				createdCount++
			}
			returnedIDs[result.ID]++
		}()
	}
	close(start)
	wait.Wait()
	if len(errs) != 0 || createdCount != 1 || len(returnedIDs) != 1 {
		t.Fatalf("concurrent creates errors=%v created=%d ids=%v", errs, createdCount, returnedIDs)
	}
	if storedRun, err := repository.RunByID(ctx, draftRun.ID); err != nil || storedRun.VersionID != "" {
		t.Fatalf("stored draft run = %+v, err %v", storedRun, err)
	}
	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	draftRun.Status, draftRun.StartedAt = "running", &startedAt
	startedEvent := domain.Event{
		SchemaVersion: domain.SchemaVersion, Type: "run.started", RunID: draftRun.ID,
		Sequence: 1, OccurredAt: startedAt, Payload: map[string]any{},
	}
	gapEvent := startedEvent
	gapEvent.Sequence = 2
	if err := repository.AppendRunEvent(ctx, draftRun, gapEvent); !errors.Is(err, ErrConflict) {
		t.Fatalf("gap event error=%v, want ErrConflict", err)
	}
	if err := repository.AppendRunEvent(ctx, draftRun, startedEvent); err != nil {
		t.Fatalf("append one run event: %v", err)
	}
	if err := repository.AppendRunEvent(ctx, draftRun, startedEvent); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate event error=%v, want ErrConflict", err)
	}
	appended, err := repository.RunByID(ctx, draftRun.ID)
	if err != nil || appended.Status != "running" || len(appended.Events) != 1 || appended.Events[0].Sequence != 1 {
		t.Fatalf("incrementally persisted run = %+v, err %v", appended, err)
	}
	var payload []byte
	if err := repository.pool.QueryRow(ctx, `SELECT payload FROM runs WHERE id=$1`, draftRun.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(`"events"`)) || bytes.Contains(payload, []byte(`"nodeRuns"`)) {
		t.Fatalf("normalized event history leaked into runs.payload: %s", payload)
	}

	concurrentAppendRun := draftRun
	concurrentAppendRun.ID, concurrentAppendRun.Status = uuid.NewString(), "queued"
	concurrentAppendRun.StartedAt, concurrentAppendRun.Events = nil, nil
	concurrentAppendRun.CreatedAt = time.Now().UTC()
	if _, created, err := repository.CreateRun(ctx, concurrentAppendRun, RunIdempotency{}); err != nil || !created {
		t.Fatalf("create concurrent append run: created=%v err=%v", created, err)
	}
	concurrentOccurredAt := time.Now().UTC()
	concurrentAppendRun.Status, concurrentAppendRun.StartedAt = "running", &concurrentOccurredAt
	concurrentEvent := domain.Event{
		SchemaVersion: domain.SchemaVersion, Type: "run.started", RunID: concurrentAppendRun.ID,
		Sequence: 1, OccurredAt: concurrentOccurredAt, Payload: map[string]any{},
	}
	appendStart := make(chan struct{})
	appendErrors := make(chan error, 2)
	for range 2 {
		go func() {
			<-appendStart
			appendErrors <- repository.AppendRunEvent(ctx, concurrentAppendRun, concurrentEvent)
		}()
	}
	close(appendStart)
	appendSuccesses, appendConflicts := 0, 0
	for range 2 {
		switch appendErr := <-appendErrors; {
		case appendErr == nil:
			appendSuccesses++
		case errors.Is(appendErr, ErrConflict):
			appendConflicts++
		default:
			t.Fatalf("concurrent append error=%v", appendErr)
		}
	}
	concurrentlyAppended, err := repository.RunByID(ctx, concurrentAppendRun.ID)
	if err != nil || appendSuccesses != 1 || appendConflicts != 1 || len(concurrentlyAppended.Events) != 1 {
		t.Fatalf("concurrent append run=%+v successes=%d conflicts=%d err=%v",
			concurrentlyAppended, appendSuccesses, appendConflicts, err)
	}

	legacyRun := draftRun
	legacyRun.ID, legacyRun.Status, legacyRun.CreatedAt = uuid.NewString(), "running", time.Now().UTC()
	legacyRun.StartedAt, legacyRun.Events = &legacyRun.CreatedAt, []domain.Event{{
		SchemaVersion: domain.SchemaVersion, Type: "run.started", RunID: legacyRun.ID,
		Sequence: 1, OccurredAt: legacyRun.CreatedAt, Payload: map[string]any{},
	}}
	if _, created, err := repository.CreateRun(ctx, legacyRun, RunIdempotency{}); err != nil || !created {
		t.Fatalf("create legacy compatibility run: created=%v err=%v", created, err)
	}
	legacyRun.Events = append(legacyRun.Events, domain.Event{
		SchemaVersion: domain.SchemaVersion, Type: "node.started", RunID: legacyRun.ID,
		Sequence: 2, OccurredAt: legacyRun.CreatedAt.Add(time.Millisecond), Payload: map[string]any{},
	})
	legacyPayload, err := json.Marshal(legacyRun)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.pool.Exec(ctx, `UPDATE runs SET payload=$2 WHERE id=$1`, legacyRun.ID, legacyPayload); err != nil {
		t.Fatal(err)
	}
	legacyNextEvent := domain.Event{
		SchemaVersion: domain.SchemaVersion, Type: "node.completed", RunID: legacyRun.ID,
		Sequence: 3, OccurredAt: legacyRun.CreatedAt.Add(2 * time.Millisecond), Payload: map[string]any{},
	}
	if err := repository.AppendRunEvent(ctx, legacyRun, legacyNextEvent); err != nil {
		t.Fatalf("append after partial legacy history: %v", err)
	}
	legacyStored, err := repository.RunByID(ctx, legacyRun.ID)
	if err != nil || len(legacyStored.Events) != 3 || legacyStored.Events[2].Sequence != 3 {
		t.Fatalf("partial legacy run=%+v err=%v", legacyStored, err)
	}

	terminalRun := draftRun
	terminalRun.ID, terminalRun.Status, terminalRun.StartedAt = uuid.NewString(), "queued", nil
	terminalRun.Output, terminalRun.NodeRuns, terminalRun.Events = nil, nil, nil
	terminalRun.CreatedAt = time.Now().UTC()
	if _, created, err := repository.CreateRun(ctx, terminalRun, RunIdempotency{}); err != nil || !created {
		t.Fatalf("create terminal test run: created=%v err=%v", created, err)
	}
	terminalStarted := startedEvent
	terminalStarted.RunID, terminalStarted.OccurredAt = terminalRun.ID, time.Now().UTC()
	terminalRun.Status, terminalRun.StartedAt = "running", &terminalStarted.OccurredAt
	if err := repository.AppendRunEvent(ctx, terminalRun, terminalStarted); err != nil {
		t.Fatal(err)
	}
	finishedAt := terminalStarted.OccurredAt.Add(time.Second)
	terminalRun.Status, terminalRun.CompletedAt = "completed", &finishedAt
	terminalRun.Output = map[string]any{"result": "ok"}
	terminalRun.NodeRuns = []domain.NodeRun{{NodeID: "start", TokenID: "token-1", Status: "success"}}
	completedEvent := domain.Event{
		SchemaVersion: domain.SchemaVersion, Type: "run.completed", RunID: terminalRun.ID,
		Sequence: 2, OccurredAt: finishedAt, LogicalTimeMS: 1000, Payload: map[string]any{},
	}
	if err := repository.AppendRunEvent(ctx, terminalRun, completedEvent); err != nil {
		t.Fatal(err)
	}
	terminalStored, err := repository.RunByID(ctx, terminalRun.ID)
	if err != nil || terminalStored.Status != "completed" || len(terminalStored.Events) != 2 ||
		len(terminalStored.NodeRuns) != 1 || terminalStored.Output["result"] != "ok" {
		t.Fatalf("terminal run = %+v, err %v", terminalStored, err)
	}
	lateEvent := completedEvent
	lateEvent.Sequence = 3
	if err := repository.AppendRunEvent(ctx, terminalRun, lateEvent); !errors.Is(err, ErrConflict) {
		t.Fatalf("append after terminal error=%v, want ErrConflict", err)
	}

	snapshotRun := terminalRun
	snapshotRun.ID, snapshotRun.CreatedAt = uuid.NewString(), time.Now().UTC()
	snapshotRun.Events = []domain.Event{{
		SchemaVersion: domain.SchemaVersion, Type: "run.completed", RunID: snapshotRun.ID,
		Sequence: 1, OccurredAt: snapshotRun.CreatedAt, Payload: map[string]any{},
	}}
	if _, created, err := repository.CreateRun(ctx, snapshotRun, RunIdempotency{}); err != nil || !created {
		t.Fatalf("create prepopulated run: created=%v err=%v", created, err)
	}
	prepopulated, err := repository.RunByID(ctx, snapshotRun.ID)
	if err != nil || len(prepopulated.Events) != 1 || len(prepopulated.NodeRuns) != 1 {
		t.Fatalf("prepopulated run = %+v, err %v", prepopulated, err)
	}
	snapshotRun.Events[0].Type = "run.failed"
	if err := repository.UpdateRun(ctx, snapshotRun); err != nil {
		t.Fatal(err)
	}
	replacedSnapshot, err := repository.RunByID(ctx, snapshotRun.ID)
	if err != nil || len(replacedSnapshot.Events) != 1 || replacedSnapshot.Events[0].Type != "run.failed" {
		t.Fatalf("replaced run = %+v, err %v", replacedSnapshot, err)
	}
	snapshotRun.Events, snapshotRun.NodeRuns = nil, nil
	if err := repository.UpdateRun(ctx, snapshotRun); err != nil {
		t.Fatal(err)
	}
	cleared, err := repository.RunByID(ctx, snapshotRun.ID)
	if err != nil || len(cleared.Events) != 0 || len(cleared.NodeRuns) != 0 {
		t.Fatalf("cleared run = %+v, err %v", cleared, err)
	}
	recoveredAt := time.Now().UTC().Truncate(time.Microsecond)
	if count, err := repository.InterruptActiveRuns(ctx, recoveredAt); err != nil || count < 1 {
		t.Fatalf("startup recovery count=%d err=%v", count, err)
	}
	recovered, err := repository.RunByID(ctx, draftRun.ID)
	if err != nil || recovered.Status != "interrupted" || len(recovered.Events) != 2 ||
		recovered.Events[1].Type != "run.interrupted" || recovered.Events[1].Sequence != 2 {
		t.Fatalf("recovered draft run = %+v, err %v", recovered, err)
	}
}
