package store

import (
	"context"
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
	recoveredAt := time.Now().UTC().Truncate(time.Microsecond)
	if count, err := repository.InterruptActiveRuns(ctx, recoveredAt); err != nil || count < 1 {
		t.Fatalf("startup recovery count=%d err=%v", count, err)
	}
	recovered, err := repository.RunByID(ctx, draftRun.ID)
	if err != nil || recovered.Status != "interrupted" || len(recovered.Events) != 1 ||
		recovered.Events[0].Type != "run.interrupted" || recovered.Events[0].Sequence != 1 {
		t.Fatalf("recovered draft run = %+v, err %v", recovered, err)
	}
}
