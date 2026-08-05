package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
)

func TestMemoryIsolationETagAndIdempotency(t *testing.T) {
	ctx := context.Background()
	repository := NewMemory()
	user := domain.User{ID: "user-1", Email: "owner@example.com", CreatedAt: time.Now()}
	if err := repository.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Name: "Project", OwnerID: user.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := repository.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	role, err := repository.ProjectRole(ctx, project.ID, user.ID)
	if err != nil || role != domain.RoleOwner {
		t.Fatalf("role = %s, err %v", role, err)
	}
	definition := domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion, Name: "Flow", Variables: []domain.VariableDefinition{},
		Layout: domain.Layout{Mode: "force"}, Nodes: []domain.Node{}, Edges: []domain.Edge{},
	}
	flow := domain.Flow{ID: "flow-1", ProjectID: project.ID, Name: "Flow", Draft: definition, DraftETag: `"one"`}
	if err := repository.CreateFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	copy, err := repository.FlowByID(ctx, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	copy.Draft.Name = "Changed outside store"
	unchanged, _ := repository.FlowByID(ctx, flow.ID)
	if unchanged.Draft.Name != "Flow" {
		t.Fatal("repository leaked mutable state")
	}
	copy = unchanged
	copy.DraftETag = `"two"`
	if err := repository.UpdateFlow(ctx, copy, `"stale"`); err != ErrPrecondition {
		t.Fatalf("stale update err = %v", err)
	}
	if err := repository.UpdateFlow(ctx, copy, `"one"`); err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "run-1", ProjectID: project.ID, FlowID: flow.ID, Status: "created", CreatedAt: time.Now()}
	idempotency := RunIdempotency{
		UserID: user.ID, TargetType: "flow_draft", TargetID: flow.ID,
		TargetRevision: flow.DraftETag, Key: "stable-key", RequestHash: strings.Repeat("a", 64),
	}
	if _, created, err := repository.CreateRun(ctx, run, idempotency); err != nil || !created {
		t.Fatal(err)
	}
	replayed, created, err := repository.CreateRun(ctx,
		domain.Run{ID: "run-2", ProjectID: project.ID, FlowID: flow.ID, CreatedAt: time.Now()}, idempotency)
	if err != nil || created || replayed.ID != run.ID {
		t.Fatalf("idempotent replay = %+v created=%v err=%v", replayed, created, err)
	}
	idempotency.RequestHash = strings.Repeat("b", 64)
	if _, _, err := repository.CreateRun(ctx,
		domain.Run{ID: "run-3", ProjectID: project.ID, FlowID: flow.ID, CreatedAt: time.Now()}, idempotency); err != ErrIdempotencyMismatch {
		t.Fatalf("idempotency mismatch err = %v", err)
	}
}

func TestEmbeddedMigrationContainsCoreTables(t *testing.T) {
	raw, err := migrationFiles.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, table := range []string{"users", "projects", "flows", "flow_versions", "runs", "run_idempotency_keys", "run_events", "node_runs", "share_links"} {
		if !strings.Contains(sql, "TABLE IF NOT EXISTS "+table) {
			t.Errorf("migration does not define %s", table)
		}
	}
	if !strings.Contains(sql, "draft jsonb") || !strings.Contains(sql, "definition jsonb") {
		t.Fatal("flow documents are not persisted as JSONB")
	}
}

func TestMemoryListsVersionsNewestFirst(t *testing.T) {
	repository := NewMemory()
	ctx := context.Background()
	for _, number := range []int{2, 1, 3} {
		if err := repository.CreateVersion(ctx, domain.FlowVersion{
			ID: "version-" + string(rune('0'+number)), FlowID: "flow-1", Number: number,
		}); err != nil {
			t.Fatal(err)
		}
	}
	versions, err := repository.ListVersions(ctx, "flow-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 || versions[0].Number != 3 || versions[1].Number != 2 || versions[2].Number != 1 {
		t.Fatalf("versions are not newest first: %#v", versions)
	}
}

func TestMemoryRunIdempotencyExpiresWithoutSliding(t *testing.T) {
	repository := NewMemory()
	current := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	repository.now = func() time.Time { return current }
	idempotency := RunIdempotency{
		UserID: "user-1", TargetType: "flow_version", TargetID: "version-1",
		Key: "retention-key", RequestHash: strings.Repeat("a", 64),
	}
	original := domain.Run{ID: "run-original", CreatedAt: current}
	if _, created, err := repository.CreateRun(context.Background(), original, idempotency); err != nil || !created {
		t.Fatalf("create original: created=%v err=%v", created, err)
	}

	current = current.Add(RunIdempotencyRetention - time.Nanosecond)
	replayed, created, err := repository.CreateRun(context.Background(),
		domain.Run{ID: "run-before-expiry", CreatedAt: current}, idempotency)
	if err != nil || created || replayed.ID != original.ID {
		t.Fatalf("replay before expiry=%+v created=%v err=%v", replayed, created, err)
	}

	current = current.Add(time.Nanosecond)
	idempotency.RequestHash = strings.Repeat("b", 64)
	replacement := domain.Run{ID: "run-replacement", CreatedAt: current}
	stored, created, err := repository.CreateRun(context.Background(), replacement, idempotency)
	if err != nil || !created || stored.ID != replacement.ID {
		t.Fatalf("replacement at expiry=%+v created=%v err=%v", stored, created, err)
	}
	if _, err := repository.RunByID(context.Background(), original.ID); err != nil {
		t.Fatalf("expired claim removed its original run: %v", err)
	}
}
