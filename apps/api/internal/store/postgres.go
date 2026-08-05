package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/flowverse/flowverse-api/internal/domain"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Postgres struct {
	pool *pgxpool.Pool
}

func OpenPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	repository := &Postgres{pool: pool}
	if err := repository.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return repository, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

func (p *Postgres) Migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, readErr := migrationFiles.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			return readErr
		}
		if _, execErr := p.pool.Exec(ctx, string(raw)); execErr != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), execErr)
		}
	}
	return nil
}

func (p *Postgres) CreateUser(ctx context.Context, user domain.User) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO users(id,email,display_name,password_hash,created_at) VALUES($1,$2,$3,$4,$5)`,
		user.ID, strings.ToLower(strings.TrimSpace(user.Email)), user.DisplayName, user.PasswordHash, user.CreatedAt)
	return translate(err)
}

func (p *Postgres) UserByID(ctx context.Context, id string) (domain.User, error) {
	return p.scanUser(p.pool.QueryRow(ctx, `SELECT id,email,display_name,password_hash,created_at FROM users WHERE id=$1`, id))
}

func (p *Postgres) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	return p.scanUser(p.pool.QueryRow(ctx, `SELECT id,email,display_name,password_hash,created_at FROM users WHERE email=$1`, strings.ToLower(strings.TrimSpace(email))))
}

func (p *Postgres) scanUser(row pgx.Row) (domain.User, error) {
	var user domain.User
	err := row.Scan(&user.ID, &user.Email, &user.DisplayName, &user.PasswordHash, &user.CreatedAt)
	return user, translate(err)
}

func (p *Postgres) SaveSession(ctx context.Context, session domain.Session) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO auth_sessions(token_hash,user_id,kind,expires_at,revoked_at,family_id)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(token_hash) DO UPDATE SET expires_at=excluded.expires_at,revoked_at=excluded.revoked_at`,
		session.TokenHash, session.UserID, session.Kind, session.ExpiresAt, session.RevokedAt, session.FamilyID)
	return translate(err)
}

func (p *Postgres) SessionByHash(ctx context.Context, hash string) (domain.Session, error) {
	var session domain.Session
	err := p.pool.QueryRow(ctx, `SELECT token_hash,user_id,kind,expires_at,revoked_at,family_id FROM auth_sessions WHERE token_hash=$1`, hash).
		Scan(&session.TokenHash, &session.UserID, &session.Kind, &session.ExpiresAt, &session.RevokedAt, &session.FamilyID)
	return session, translate(err)
}

func (p *Postgres) RevokeSession(ctx context.Context, hash string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE auth_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE token_hash=$1`, hash)
	return translatedRows(tag.RowsAffected(), err)
}

func (p *Postgres) RevokeSessionFamily(ctx context.Context, family string) error {
	_, err := p.pool.Exec(ctx, `UPDATE auth_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE family_id=$1`, family)
	return translate(err)
}

func (p *Postgres) CreateProject(ctx context.Context, project domain.Project) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO projects(id,name,description,owner_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6)`,
		project.ID, project.Name, project.Description, project.OwnerID, project.CreatedAt, project.UpdatedAt); err != nil {
		return translate(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_members(project_id,user_id,role) VALUES($1,$2,'owner')`, project.ID, project.OwnerID); err != nil {
		return translate(err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListProjects(ctx context.Context, userID string) ([]domain.Project, error) {
	rows, err := p.pool.Query(ctx, `SELECT p.id,p.name,p.description,p.owner_id,p.created_at,p.updated_at
		FROM projects p JOIN project_members m ON m.project_id=p.id WHERE m.user_id=$1 ORDER BY p.created_at`, userID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := []domain.Project{}
	for rows.Next() {
		var project domain.Project
		if err := rows.Scan(&project.ID, &project.Name, &project.Description, &project.OwnerID, &project.CreatedAt, &project.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, project)
	}
	return result, rows.Err()
}

func (p *Postgres) ProjectByID(ctx context.Context, id string) (domain.Project, error) {
	var project domain.Project
	err := p.pool.QueryRow(ctx, `SELECT id,name,description,owner_id,created_at,updated_at FROM projects WHERE id=$1`, id).
		Scan(&project.ID, &project.Name, &project.Description, &project.OwnerID, &project.CreatedAt, &project.UpdatedAt)
	return project, translate(err)
}

func (p *Postgres) UpdateProject(ctx context.Context, project domain.Project) error {
	tag, err := p.pool.Exec(ctx, `UPDATE projects SET name=$2,description=$3,updated_at=$4 WHERE id=$1`,
		project.ID, project.Name, project.Description, project.UpdatedAt)
	return translatedRows(tag.RowsAffected(), err)
}

func (p *Postgres) DeleteProject(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, id)
	return translatedRows(tag.RowsAffected(), err)
}

func (p *Postgres) SetProjectMember(ctx context.Context, projectID, userID string, role domain.Role) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO project_members(project_id,user_id,role) VALUES($1,$2,$3)
		ON CONFLICT(project_id,user_id) DO UPDATE SET role=excluded.role`, projectID, userID, role)
	return translate(err)
}

func (p *Postgres) RemoveProjectMember(ctx context.Context, projectID, userID string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM project_members WHERE project_id=$1 AND user_id=$2 AND role<>'owner'`, projectID, userID)
	return translatedRows(tag.RowsAffected(), err)
}

func (p *Postgres) ProjectRole(ctx context.Context, projectID, userID string) (domain.Role, error) {
	var role domain.Role
	err := p.pool.QueryRow(ctx, `SELECT role FROM project_members WHERE project_id=$1 AND user_id=$2`, projectID, userID).Scan(&role)
	return role, translate(err)
}

func (p *Postgres) ListProjectMembers(ctx context.Context, projectID string) (map[string]domain.Role, error) {
	rows, err := p.pool.Query(ctx, `SELECT user_id,role FROM project_members WHERE project_id=$1`, projectID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := map[string]domain.Role{}
	for rows.Next() {
		var id string
		var role domain.Role
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		result[id] = role
	}
	return result, rows.Err()
}

func (p *Postgres) CreateFlow(ctx context.Context, flow domain.Flow) error {
	raw, err := json.Marshal(flow.Draft)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO flows(id,project_id,name,description,draft,draft_etag,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, flow.ID, flow.ProjectID, flow.Name, flow.Description, raw, flow.DraftETag, flow.CreatedAt, flow.UpdatedAt)
	return translate(err)
}

func (p *Postgres) ListFlows(ctx context.Context, projectID string) ([]domain.Flow, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,project_id,name,description,draft,draft_etag,created_at,updated_at
		FROM flows WHERE project_id=$1 AND deleted_at IS NULL ORDER BY created_at`, projectID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := []domain.Flow{}
	for rows.Next() {
		flow, scanErr := scanFlow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, flow)
	}
	return result, rows.Err()
}

func (p *Postgres) FlowByID(ctx context.Context, id string) (domain.Flow, error) {
	flow, err := scanFlow(p.pool.QueryRow(ctx, `SELECT id,project_id,name,description,draft,draft_etag,created_at,updated_at
		FROM flows WHERE id=$1 AND deleted_at IS NULL`, id))
	return flow, translate(err)
}

type rowScanner interface{ Scan(...any) error }

func scanFlow(row rowScanner) (domain.Flow, error) {
	var flow domain.Flow
	var raw []byte
	if err := row.Scan(&flow.ID, &flow.ProjectID, &flow.Name, &flow.Description, &raw, &flow.DraftETag, &flow.CreatedAt, &flow.UpdatedAt); err != nil {
		return flow, err
	}
	return flow, json.Unmarshal(raw, &flow.Draft)
}

func (p *Postgres) UpdateFlow(ctx context.Context, flow domain.Flow, expectedETag string) error {
	raw, err := json.Marshal(flow.Draft)
	if err != nil {
		return err
	}
	tag, err := p.pool.Exec(ctx, `UPDATE flows SET name=$2,description=$3,draft=$4,draft_etag=$5,updated_at=$6
		WHERE id=$1 AND draft_etag=$7 AND deleted_at IS NULL`, flow.ID, flow.Name, flow.Description, raw, flow.DraftETag, flow.UpdatedAt, expectedETag)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		if _, findErr := p.FlowByID(ctx, flow.ID); findErr != nil {
			return findErr
		}
		return ErrPrecondition
	}
	return nil
}

func (p *Postgres) DeleteFlow(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE flows SET deleted_at=now() WHERE id=$1 AND deleted_at IS NULL`, id)
	return translatedRows(tag.RowsAffected(), err)
}

func (p *Postgres) CreateVersion(ctx context.Context, version domain.FlowVersion) error {
	raw, err := json.Marshal(version.Definition)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO flow_versions(id,flow_id,version_number,definition,checksum,created_at,published_by)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, version.ID, version.FlowID, version.Number, raw, version.Checksum, version.CreatedAt, version.PublishedBy)
	return translate(err)
}

func (p *Postgres) ListVersions(ctx context.Context, flowID string) ([]domain.FlowVersion, error) {
	rows, err := p.pool.Query(ctx, `SELECT id,flow_id,version_number,definition,checksum,created_at,published_by
		FROM flow_versions WHERE flow_id=$1 ORDER BY version_number DESC`, flowID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := []domain.FlowVersion{}
	for rows.Next() {
		version, scanErr := scanVersion(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, version)
	}
	return result, rows.Err()
}

func (p *Postgres) VersionByID(ctx context.Context, id string) (domain.FlowVersion, error) {
	version, err := scanVersion(p.pool.QueryRow(ctx, `SELECT id,flow_id,version_number,definition,checksum,created_at,published_by FROM flow_versions WHERE id=$1`, id))
	return version, translate(err)
}

func scanVersion(row rowScanner) (domain.FlowVersion, error) {
	var version domain.FlowVersion
	var raw []byte
	if err := row.Scan(&version.ID, &version.FlowID, &version.Number, &raw, &version.Checksum, &version.CreatedAt, &version.PublishedBy); err != nil {
		return version, err
	}
	return version, json.Unmarshal(raw, &version.Definition)
}

func marshalRunPayload(run domain.Run) ([]byte, error) {
	// Events and node visits have dedicated ordered tables. Keeping them out of
	// the JSON snapshot makes the payload size independent from replay length.
	run.Events = nil
	run.NodeRuns = nil
	return json.Marshal(run)
}

func marshalOptionalJSON(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

func decodeRun(
	raw []byte,
	status string,
	startedAt, completedAt *time.Time,
	outputRaw []byte,
	runError string,
	eventsRaw, nodeRunsRaw []byte,
) (domain.Run, error) {
	var run domain.Run
	if err := json.Unmarshal(raw, &run); err != nil {
		return domain.Run{}, err
	}
	run.Status = status
	if startedAt != nil {
		run.StartedAt = startedAt
	}
	if completedAt != nil {
		run.CompletedAt = completedAt
	}
	if len(outputRaw) > 0 {
		if err := json.Unmarshal(outputRaw, &run.Output); err != nil {
			return domain.Run{}, err
		}
	}
	if runError != "" {
		run.Error = runError
	}
	legacyEvents, legacyNodeRuns := run.Events, run.NodeRuns
	var events []domain.Event
	if err := json.Unmarshal(eventsRaw, &events); err != nil {
		return domain.Run{}, err
	}
	var nodeRuns []domain.NodeRun
	if err := json.Unmarshal(nodeRunsRaw, &nodeRuns); err != nil {
		return domain.Run{}, err
	}
	run.Events = mergeRunEvents(legacyEvents, events)
	if len(nodeRuns) > 0 || len(legacyNodeRuns) == 0 {
		run.NodeRuns = nodeRuns
	} else {
		run.NodeRuns = legacyNodeRuns
	}
	return run, nil
}

func mergeRunEvents(legacy, normalized []domain.Event) []domain.Event {
	if len(legacy) == 0 {
		return normalized
	}
	if len(normalized) == 0 {
		return legacy
	}
	bySequence := make(map[int64]domain.Event, len(legacy)+len(normalized))
	for _, event := range legacy {
		bySequence[event.Sequence] = event
	}
	// The normalized table is authoritative when both representations contain
	// the same sequence during a rolling upgrade from the legacy payload.
	for _, event := range normalized {
		bySequence[event.Sequence] = event
	}
	merged := make([]domain.Event, 0, len(bySequence))
	for _, event := range bySequence {
		merged = append(merged, event)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Sequence < merged[j].Sequence })
	return merged
}

func (p *Postgres) CreateRun(ctx context.Context, run domain.Run, idempotency RunIdempotency) (domain.Run, bool, error) {
	if idempotency.Key != "" {
		existing, found, err := p.resolveRunIdempotency(ctx, idempotency)
		if err != nil {
			return domain.Run{}, false, err
		}
		if found {
			return existing, false, nil
		}
	}
	raw, err := marshalRunPayload(run)
	if err != nil {
		return domain.Run{}, false, err
	}
	outputRaw, err := marshalOptionalJSON(run.Output)
	if err != nil {
		return domain.Run{}, false, err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return domain.Run{}, false, err
	}
	defer tx.Rollback(ctx)
	if idempotency.Key != "" {
		if _, err = tx.Exec(ctx, `DELETE FROM run_idempotency_keys
			WHERE user_id=$1 AND target_type=$2 AND target_id=$3 AND target_revision=$4
			  AND idempotency_key=$5 AND expires_at <= now()`,
			idempotency.UserID, idempotency.TargetType, idempotency.TargetID,
			idempotency.TargetRevision, idempotency.Key); err != nil {
			return domain.Run{}, false, translate(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO runs(
		id,project_id,flow_id,version_id,status,payload,started_at,completed_at,output,error,created_at
	) VALUES($1,$2,$3,NULLIF($4::text,'')::uuid,$5,$6,$7,$8,$9,$10,$11)`,
		run.ID, run.ProjectID, run.FlowID, run.VersionID, run.Status, raw,
		run.StartedAt, run.CompletedAt, outputRaw, run.Error, run.CreatedAt); err != nil {
		return domain.Run{}, false, translate(err)
	}
	for _, event := range run.Events {
		eventRaw, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return domain.Run{}, false, marshalErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO run_events(run_id,sequence,event,occurred_at) VALUES($1,$2,$3,$4)`,
			run.ID, event.Sequence, eventRaw, event.OccurredAt); err != nil {
			return domain.Run{}, false, translate(err)
		}
	}
	for index, nodeRun := range run.NodeRuns {
		nodeRaw, marshalErr := json.Marshal(nodeRun)
		if marshalErr != nil {
			return domain.Run{}, false, marshalErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO node_runs(run_id,ordinal,node_run) VALUES($1,$2,$3)`,
			run.ID, index, nodeRaw); err != nil {
			return domain.Run{}, false, translate(err)
		}
	}
	if idempotency.Key != "" {
		_, err = tx.Exec(ctx, `INSERT INTO run_idempotency_keys(
				user_id,target_type,target_id,target_revision,idempotency_key,request_hash,
				run_id,created_at,expires_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,now(),now()+interval '24 hours')`,
			idempotency.UserID, idempotency.TargetType, idempotency.TargetID,
			idempotency.TargetRevision, idempotency.Key, idempotency.RequestHash,
			run.ID)
		if err != nil {
			if isUniqueViolation(err) {
				_ = tx.Rollback(ctx)
				existing, found, resolveErr := p.resolveRunIdempotency(ctx, idempotency)
				if resolveErr != nil {
					return domain.Run{}, false, resolveErr
				}
				if found {
					return existing, false, nil
				}
			}
			return domain.Run{}, false, translate(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Run{}, false, translate(err)
	}
	return run, true, nil
}

func (p *Postgres) resolveRunIdempotency(ctx context.Context, idempotency RunIdempotency) (domain.Run, bool, error) {
	var requestHash string
	var runID string
	err := p.pool.QueryRow(ctx, `SELECT i.request_hash,r.id
		FROM run_idempotency_keys i
		JOIN runs r ON r.id=i.run_id
		WHERE i.user_id=$1 AND i.target_type=$2 AND i.target_id=$3
		  AND i.target_revision=$4 AND i.idempotency_key=$5 AND i.expires_at > now()`,
		idempotency.UserID, idempotency.TargetType, idempotency.TargetID,
		idempotency.TargetRevision, idempotency.Key).Scan(&requestHash, &runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, false, nil
	}
	if err != nil {
		return domain.Run{}, false, translate(err)
	}
	if requestHash != idempotency.RequestHash {
		return domain.Run{}, false, ErrIdempotencyMismatch
	}
	run, err := p.RunByID(ctx, runID)
	if err != nil {
		return domain.Run{}, false, err
	}
	return run, true, nil
}

func (p *Postgres) RunByID(ctx context.Context, id string) (domain.Run, error) {
	var raw, outputRaw, eventsRaw, nodeRunsRaw []byte
	var status, runError string
	var startedAt, completedAt *time.Time
	err := p.pool.QueryRow(ctx, `SELECT r.payload,r.status,r.started_at,r.completed_at,r.output,r.error,
		COALESCE((SELECT jsonb_agg(e.event ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id),'[]'::jsonb),
		COALESCE((SELECT jsonb_agg(n.node_run ORDER BY n.ordinal) FROM node_runs n WHERE n.run_id=r.id),'[]'::jsonb)
		FROM runs r WHERE r.id=$1`, id).Scan(
		&raw, &status, &startedAt, &completedAt, &outputRaw, &runError, &eventsRaw, &nodeRunsRaw,
	)
	if err != nil {
		return domain.Run{}, translate(err)
	}
	return decodeRun(raw, status, startedAt, completedAt, outputRaw, runError, eventsRaw, nodeRunsRaw)
}

func (p *Postgres) UpdateRun(ctx context.Context, run domain.Run) error {
	raw, err := marshalRunPayload(run)
	if err != nil {
		return err
	}
	outputRaw, err := marshalOptionalJSON(run.Output)
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE runs SET
		status=$2,payload=$3,started_at=$4,completed_at=$5,output=$6,error=$7,updated_at=now()
		WHERE id=$1`, run.ID, run.Status, raw, run.StartedAt, run.CompletedAt, outputRaw, run.Error)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `DELETE FROM run_events WHERE run_id=$1`, run.ID); err != nil {
		return translate(err)
	}
	for _, event := range run.Events {
		eventRaw, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO run_events(run_id,sequence,event,occurred_at) VALUES($1,$2,$3,$4)`,
			run.ID, event.Sequence, eventRaw, event.OccurredAt); err != nil {
			return translate(err)
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM node_runs WHERE run_id=$1`, run.ID); err != nil {
		return translate(err)
	}
	if len(run.NodeRuns) > 0 {
		for index, nodeRun := range run.NodeRuns {
			nodeRaw, marshalErr := json.Marshal(nodeRun)
			if marshalErr != nil {
				return marshalErr
			}
			if _, err = tx.Exec(ctx, `INSERT INTO node_runs(run_id,ordinal,node_run) VALUES($1,$2,$3)`, run.ID, index, nodeRaw); err != nil {
				return translate(err)
			}
		}
	}
	return tx.Commit(ctx)
}

// AppendRunEvent updates only the mutable run fields and inserts one event.
// Event history and node visits live in their normalized tables, so playback
// no longer serializes and reinserts the full history after every step.
func (p *Postgres) AppendRunEvent(ctx context.Context, run domain.Run, event domain.Event) error {
	outputRaw, err := marshalOptionalJSON(run.Output)
	if err != nil {
		return err
	}
	eventRaw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentStatus string
	var legacyMaxSequence int64
	if err := tx.QueryRow(ctx, `SELECT status,COALESCE((
		SELECT MAX((legacy_event->>'sequence')::bigint)
		FROM jsonb_array_elements(CASE
			WHEN jsonb_typeof(payload->'events')='array' THEN payload->'events'
			ELSE '[]'::jsonb
		END) AS legacy_event
	),0)
		FROM runs WHERE id=$1 FOR UPDATE`, run.ID).Scan(&currentStatus, &legacyMaxSequence); err != nil {
		return translate(err)
	}
	if !activeRunStatus(currentStatus) {
		return ErrConflict
	}
	var normalizedMaxSequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0) FROM run_events WHERE run_id=$1`, run.ID).Scan(&normalizedMaxSequence); err != nil {
		return translate(err)
	}
	if normalizedMaxSequence > legacyMaxSequence {
		legacyMaxSequence = normalizedMaxSequence
	}
	expectedSequence := legacyMaxSequence + 1
	if event.Sequence != expectedSequence {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE runs SET
		status=$2,started_at=$3,completed_at=$4,output=$5,error=$6,updated_at=now()
		WHERE id=$1`, run.ID, run.Status, run.StartedAt, run.CompletedAt, outputRaw, run.Error)
	if err != nil {
		return translate(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO run_events(run_id,sequence,event,occurred_at)
		VALUES($1,$2,$3,$4)`, run.ID, event.Sequence, eventRaw, event.OccurredAt); err != nil {
		return translate(err)
	}
	if len(run.NodeRuns) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM node_runs WHERE run_id=$1`, run.ID); err != nil {
			return translate(err)
		}
		for index, nodeRun := range run.NodeRuns {
			nodeRaw, marshalErr := json.Marshal(nodeRun)
			if marshalErr != nil {
				return marshalErr
			}
			if _, err = tx.Exec(ctx, `INSERT INTO node_runs(run_id,ordinal,node_run) VALUES($1,$2,$3)`, run.ID, index, nodeRaw); err != nil {
				return translate(err)
			}
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListRuns(ctx context.Context, flowID string) ([]domain.Run, error) {
	rows, err := p.pool.Query(ctx, `SELECT r.payload,r.status,r.started_at,r.completed_at,r.output,r.error,
		COALESCE((SELECT jsonb_agg(e.event ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id),'[]'::jsonb),
		COALESCE((SELECT jsonb_agg(n.node_run ORDER BY n.ordinal) FROM node_runs n WHERE n.run_id=r.id),'[]'::jsonb)
		FROM runs r WHERE r.flow_id=$1 ORDER BY r.created_at DESC`, flowID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := []domain.Run{}
	for rows.Next() {
		var raw, outputRaw, eventsRaw, nodeRunsRaw []byte
		var status, runError string
		var startedAt, completedAt *time.Time
		if err := rows.Scan(
			&raw, &status, &startedAt, &completedAt, &outputRaw, &runError, &eventsRaw, &nodeRunsRaw,
		); err != nil {
			return nil, err
		}
		run, decodeErr := decodeRun(
			raw, status, startedAt, completedAt, outputRaw, runError, eventsRaw, nodeRunsRaw,
		)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

func (p *Postgres) InterruptActiveRuns(ctx context.Context, occurredAt time.Time) (int, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,status,payload FROM runs
		WHERE status = ANY($1::text[]) FOR UPDATE`,
		[]string{"created", "queued", "running", "paused", "waiting"})
	if err != nil {
		return 0, translate(err)
	}
	type persistedRun struct {
		id     string
		status string
		raw    []byte
	}
	persisted := []persistedRun{}
	for rows.Next() {
		var item persistedRun
		if err := rows.Scan(&item.id, &item.status, &item.raw); err != nil {
			rows.Close()
			return 0, err
		}
		persisted = append(persisted, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	interruptedCount := 0
	for _, item := range persisted {
		var run domain.Run
		if err := json.Unmarshal(item.raw, &run); err != nil {
			return 0, fmt.Errorf("decode active run %s: %w", item.id, err)
		}
		run.ID, run.Status = item.id, item.status
		var lastEventRaw []byte
		lastEventErr := tx.QueryRow(ctx, `SELECT event FROM run_events WHERE run_id=$1 ORDER BY sequence DESC LIMIT 1`, run.ID).Scan(&lastEventRaw)
		if lastEventErr == nil {
			var lastEvent domain.Event
			if err := json.Unmarshal(lastEventRaw, &lastEvent); err != nil {
				return 0, fmt.Errorf("decode last event for active run %s: %w", item.id, err)
			}
			lastPayloadSequence := int64(0)
			for _, candidate := range run.Events {
				if candidate.Sequence > lastPayloadSequence {
					lastPayloadSequence = candidate.Sequence
				}
			}
			if lastEvent.Sequence > lastPayloadSequence {
				run.Events = append(run.Events, lastEvent)
			}
		} else if !errors.Is(lastEventErr, pgx.ErrNoRows) {
			return 0, translate(lastEventErr)
		}
		run = interruptRun(run, occurredAt)
		event := &run.Events[len(run.Events)-1]
		eventRaw, err := json.Marshal(event)
		if err != nil {
			return 0, err
		}
		tag, err := tx.Exec(ctx, `UPDATE runs
			SET status='interrupted',completed_at=$2,error=$3,updated_at=now()
			WHERE id=$1 AND status = ANY($4::text[])`,
			run.ID, run.CompletedAt, run.Error, []string{"created", "queued", "running", "paused", "waiting"})
		if err != nil {
			return 0, translate(err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO run_events(run_id,sequence,event,occurred_at)
			VALUES($1,$2,$3,$4)`, run.ID, event.Sequence, eventRaw, event.OccurredAt); err != nil {
			return 0, translate(err)
		}
		interruptedCount++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return interruptedCount, nil
}

func (p *Postgres) AcquireRunLease(
	ctx context.Context,
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
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return RunLease{}, err
	}
	defer tx.Rollback(ctx)

	var projectID, status string
	if err := tx.QueryRow(ctx, `SELECT project_id,status FROM runs WHERE id=$1 FOR UPDATE`, request.RunID).
		Scan(&projectID, &status); err != nil {
		return RunLease{}, translate(err)
	}
	if !activeRunStatus(status) {
		return RunLease{}, ErrConflict
	}
	if err := p.lockQuotaScope(ctx, tx, "run-quota-global"); err != nil {
		return RunLease{}, err
	}
	if request.ActorID != "" {
		if err := p.lockQuotaScope(ctx, tx, "run-quota-actor:"+request.ActorID); err != nil {
			return RunLease{}, err
		}
	}
	if projectID != "" {
		if err := p.lockQuotaScope(ctx, tx, "run-quota-project:"+projectID); err != nil {
			return RunLease{}, err
		}
	}

	var existing RunLease
	lease, found, err := p.runLeaseByID(ctx, tx, request.RunID)
	if err != nil {
		return RunLease{}, err
	}
	if found {
		existing = lease
		if activeLease(existing, request.Now) {
			if existing.InstanceID != request.InstanceID {
				return RunLease{}, &RunLeaseHeldError{RetryAfter: normalizedRetryAfter(existing.ExpiresAt.Sub(request.Now))}
			}
			existing.HeartbeatAt = request.Now
			existing.ExpiresAt = request.Now.Add(request.TTL)
			if _, err := tx.Exec(ctx, `UPDATE run_leases
				SET heartbeat_at=$3,expires_at=$4,released_at=NULL
				WHERE run_id=$1 AND instance_id=$2`,
				request.RunID, request.InstanceID, existing.HeartbeatAt, existing.ExpiresAt); err != nil {
				return RunLease{}, translate(err)
			}
			return existing, tx.Commit(ctx)
		}
	}

	if hit, retry := p.runQuotaExceeded(ctx, tx, request.RunID, request.ActorID, projectID, limits, request.Now); hit {
		return RunLease{}, &RunQuotaError{Scope: retry.scope, RetryAfter: retry.retryAfter}
	}

	lease = RunLease{
		RunID: request.RunID, InstanceID: request.InstanceID,
		ActorID: request.ActorID, ProjectID: projectID,
		AcquiredAt: request.Now, HeartbeatAt: request.Now, ExpiresAt: request.Now.Add(request.TTL),
	}
	if found {
		_, err = tx.Exec(ctx, `UPDATE run_leases
			SET instance_id=$2,actor_id=$3,project_id=$4,acquired_at=$5,heartbeat_at=$6,expires_at=$7,released_at=NULL
			WHERE run_id=$1`,
			lease.RunID, lease.InstanceID, lease.ActorID, lease.ProjectID, lease.AcquiredAt, lease.HeartbeatAt, lease.ExpiresAt)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO run_leases(
			run_id,instance_id,actor_id,project_id,acquired_at,heartbeat_at,expires_at,released_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,NULL)`,
			lease.RunID, lease.InstanceID, lease.ActorID, lease.ProjectID, lease.AcquiredAt, lease.HeartbeatAt, lease.ExpiresAt)
	}
	if err != nil {
		return RunLease{}, translate(err)
	}
	return lease, tx.Commit(ctx)
}

func (p *Postgres) HeartbeatRunLease(ctx context.Context, request RunLeaseRequest) (RunLease, error) {
	if err := validateLeaseRequest(request); err != nil {
		return RunLease{}, err
	}
	request.Now = request.Now.UTC()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return RunLease{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM runs WHERE id=$1 FOR UPDATE`, request.RunID).Scan(&status); err != nil {
		return RunLease{}, translate(err)
	}
	if !activeRunStatus(status) {
		return RunLease{}, ErrRunLeaseLost
	}
	lease, found, err := p.runLeaseByID(ctx, tx, request.RunID)
	if err != nil {
		return RunLease{}, err
	}
	if !found || lease.InstanceID != request.InstanceID || !activeLease(lease, request.Now) {
		return RunLease{}, ErrRunLeaseLost
	}
	lease.HeartbeatAt = request.Now
	lease.ExpiresAt = request.Now.Add(request.TTL)
	tag, err := tx.Exec(ctx, `UPDATE run_leases
		SET heartbeat_at=$3,expires_at=$4
		WHERE run_id=$1 AND instance_id=$2 AND released_at IS NULL`,
		request.RunID, request.InstanceID, lease.HeartbeatAt, lease.ExpiresAt)
	if err != nil {
		return RunLease{}, translate(err)
	}
	if tag.RowsAffected() == 0 {
		return RunLease{}, ErrRunLeaseLost
	}
	return lease, tx.Commit(ctx)
}

func (p *Postgres) ReleaseRunLease(ctx context.Context, runID, instanceID string, releasedAt time.Time) error {
	if runID == "" || instanceID == "" || releasedAt.IsZero() {
		return fmt.Errorf("run id, instance id, and release time are required")
	}
	releasedAt = releasedAt.UTC()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	lease, found, err := p.runLeaseByID(ctx, tx, runID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if lease.InstanceID != instanceID {
		return ErrRunLeaseLost
	}
	if lease.ReleasedAt != nil {
		return tx.Commit(ctx)
	}
	tag, err := tx.Exec(ctx, `UPDATE run_leases
		SET released_at=$3,expires_at=$3
		WHERE run_id=$1 AND instance_id=$2 AND released_at IS NULL`,
		runID, instanceID, releasedAt)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRunLeaseLost
	}
	return tx.Commit(ctx)
}

func (p *Postgres) InterruptExpiredRunLeases(ctx context.Context, occurredAt time.Time) (int, error) {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	} else {
		occurredAt = occurredAt.UTC()
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT
		r.id,r.payload,r.status,r.started_at,r.completed_at,r.output,r.error,
		COALESCE((SELECT jsonb_agg(e.event ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id),'[]'::jsonb),
		COALESCE((SELECT jsonb_agg(n.node_run ORDER BY n.ordinal) FROM node_runs n WHERE n.run_id=r.id),'[]'::jsonb)
		FROM run_leases rl
		JOIN runs r ON r.id=rl.run_id
		WHERE rl.released_at IS NULL
		  AND rl.expires_at <= $1
		  AND r.status = ANY($2::text[])
		FOR UPDATE OF rl, r`,
		occurredAt, []string{"created", "queued", "running", "paused", "waiting"},
	)
	if err != nil {
		return 0, translate(err)
	}
	type expiredRun struct {
		id          string
		raw         []byte
		status      string
		startedAt   *time.Time
		completedAt *time.Time
		outputRaw   []byte
		runError    string
		eventsRaw   []byte
		nodeRunsRaw []byte
	}
	persisted := []expiredRun{}
	for rows.Next() {
		var item expiredRun
		if err := rows.Scan(&item.id, &item.raw, &item.status, &item.startedAt, &item.completedAt, &item.outputRaw, &item.runError, &item.eventsRaw, &item.nodeRunsRaw); err != nil {
			rows.Close()
			return 0, err
		}
		persisted = append(persisted, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	interrupted := 0
	for _, item := range persisted {
		run, decodeErr := decodeRun(item.raw, item.status, item.startedAt, item.completedAt, item.outputRaw, item.runError, item.eventsRaw, item.nodeRunsRaw)
		if decodeErr != nil {
			return 0, decodeErr
		}
		run.ID = item.id
		run.Status = item.status
		run = interruptRun(run, occurredAt)
		event := run.Events[len(run.Events)-1]
		eventRaw, err := json.Marshal(event)
		if err != nil {
			return 0, err
		}
		tag, err := tx.Exec(ctx, `UPDATE runs
			SET status='interrupted',completed_at=$2,error=$3,updated_at=now()
			WHERE id=$1 AND status = ANY($4::text[])`,
			run.ID, run.CompletedAt, run.Error, []string{"created", "queued", "running", "paused", "waiting"})
		if err != nil {
			return 0, translate(err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO run_events(run_id,sequence,event,occurred_at)
			VALUES($1,$2,$3,$4)`, run.ID, event.Sequence, eventRaw, event.OccurredAt); err != nil {
			return 0, translate(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE run_leases SET released_at=$2,expires_at=$2
			WHERE run_id=$1 AND released_at IS NULL`, run.ID, occurredAt); err != nil {
			return 0, translate(err)
		}
		interrupted++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return interrupted, nil
}

func (p *Postgres) UpdateRunWithLease(ctx context.Context, run domain.Run, instanceID string, now time.Time) error {
	if instanceID == "" || now.IsZero() {
		return fmt.Errorf("instance id and update time are required")
	}
	now = now.UTC()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	lease, found, err := p.runLeaseByID(ctx, tx, run.ID)
	if err != nil {
		return err
	}
	if !found || lease.InstanceID != instanceID || !activeLease(lease, now) {
		return ErrRunLeaseLost
	}
	raw, err := marshalRunPayload(run)
	if err != nil {
		return err
	}
	outputRaw, err := marshalOptionalJSON(run.Output)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE runs SET
		status=$2,payload=$3,started_at=$4,completed_at=$5,output=$6,error=$7,updated_at=now()
		WHERE id=$1`, run.ID, run.Status, raw, run.StartedAt, run.CompletedAt, outputRaw, run.Error)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (p *Postgres) AppendRunEventWithLease(
	ctx context.Context,
	run domain.Run,
	event domain.Event,
	instanceID string,
	now time.Time,
) error {
	if instanceID == "" || now.IsZero() {
		return fmt.Errorf("instance id and append time are required")
	}
	now = now.UTC()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	lease, found, err := p.runLeaseByID(ctx, tx, run.ID)
	if err != nil {
		return err
	}
	if !found || lease.InstanceID != instanceID || !activeLease(lease, now) {
		return ErrRunLeaseLost
	}
	outputRaw, err := marshalOptionalJSON(run.Output)
	if err != nil {
		return err
	}
	eventRaw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var currentStatus string
	var legacyMaxSequence int64
	if err := tx.QueryRow(ctx, `SELECT status,COALESCE((
		SELECT MAX((legacy_event->>'sequence')::bigint)
		FROM jsonb_array_elements(CASE
			WHEN jsonb_typeof(payload->'events')='array' THEN payload->'events'
			ELSE '[]'::jsonb
		END) AS legacy_event
	),0)
		FROM runs WHERE id=$1 FOR UPDATE`, run.ID).Scan(&currentStatus, &legacyMaxSequence); err != nil {
		return translate(err)
	}
	if !activeRunStatus(currentStatus) {
		return ErrConflict
	}
	var normalizedMaxSequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0) FROM run_events WHERE run_id=$1`, run.ID).Scan(&normalizedMaxSequence); err != nil {
		return translate(err)
	}
	if normalizedMaxSequence > legacyMaxSequence {
		legacyMaxSequence = normalizedMaxSequence
	}
	expectedSequence := legacyMaxSequence + 1
	if event.Sequence != expectedSequence {
		return ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE runs SET
		status=$2,started_at=$3,completed_at=$4,output=$5,error=$6,updated_at=now()
		WHERE id=$1`, run.ID, run.Status, run.StartedAt, run.CompletedAt, outputRaw, run.Error)
	if err != nil {
		return translate(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO run_events(run_id,sequence,event,occurred_at)
		VALUES($1,$2,$3,$4)`, run.ID, event.Sequence, eventRaw, event.OccurredAt); err != nil {
		return translate(err)
	}
	if len(run.NodeRuns) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM node_runs WHERE run_id=$1`, run.ID); err != nil {
			return translate(err)
		}
		for index, nodeRun := range run.NodeRuns {
			nodeRaw, marshalErr := json.Marshal(nodeRun)
			if marshalErr != nil {
				return marshalErr
			}
			if _, err = tx.Exec(ctx, `INSERT INTO node_runs(run_id,ordinal,node_run) VALUES($1,$2,$3)`, run.ID, index, nodeRaw); err != nil {
				return translate(err)
			}
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListRunEvents(ctx context.Context, runID string, afterSequence int64, limit int) ([]domain.Event, error) {
	if afterSequence < 0 || limit < 1 || limit > MaxRunEventListLimit {
		return nil, fmt.Errorf("invalid run event cursor or limit")
	}
	rows, err := p.pool.Query(ctx, `SELECT event
		FROM run_events
		WHERE run_id=$1 AND sequence > $2
		ORDER BY sequence
		LIMIT $3`, runID, afterSequence, limit)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	events := make([]domain.Event, 0, limit)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var event domain.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (p *Postgres) lockQuotaScope(ctx context.Context, tx pgx.Tx, scope string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, scope)
	return translate(err)
}

type quotaCheck struct {
	scope      RunQuotaScope
	retryAfter time.Duration
}

func (p *Postgres) runQuotaExceeded(
	ctx context.Context,
	tx pgx.Tx,
	excludedRunID, actorID, projectID string,
	limits RunCapacityLimits,
	now time.Time,
) (bool, quotaCheck) {
	if limits.Global > 0 {
		if hit, retry, err := p.checkRunQuota(ctx, tx, excludedRunID, "", "", limits.Global, now); err != nil {
			return true, quotaCheck{scope: RunQuotaGlobal, retryAfter: time.Second}
		} else if hit {
			return true, quotaCheck{scope: RunQuotaGlobal, retryAfter: retry}
		}
	}
	if actorID != "" && limits.Actor > 0 {
		if hit, retry, err := p.checkRunQuota(ctx, tx, excludedRunID, actorID, "", limits.Actor, now); err != nil {
			return true, quotaCheck{scope: RunQuotaActor, retryAfter: time.Second}
		} else if hit {
			return true, quotaCheck{scope: RunQuotaActor, retryAfter: retry}
		}
	}
	if projectID != "" && limits.Project > 0 {
		if hit, retry, err := p.checkRunQuota(ctx, tx, excludedRunID, "", projectID, limits.Project, now); err != nil {
			return true, quotaCheck{scope: RunQuotaProject, retryAfter: time.Second}
		} else if hit {
			return true, quotaCheck{scope: RunQuotaProject, retryAfter: retry}
		}
	}
	return false, quotaCheck{}
}

func (p *Postgres) checkRunQuota(
	ctx context.Context,
	tx pgx.Tx,
	excludedRunID, actorID, projectID string,
	limit int,
	now time.Time,
) (bool, time.Duration, error) {
	if limit < 0 {
		return false, 0, fmt.Errorf("run capacity limits cannot be negative")
	}
	var count int
	var earliest time.Time
	statuses := []string{"created", "queued", "running", "paused", "waiting"}
	switch {
	case actorID != "":
		if err := tx.QueryRow(ctx, `SELECT COUNT(*), COALESCE(MIN(expires_at), now())
			FROM run_leases rl
			JOIN runs r ON r.id=rl.run_id
			WHERE rl.released_at IS NULL AND rl.expires_at > $1
			  AND r.status = ANY($2::text[])
			  AND rl.run_id <> $3 AND rl.actor_id = $4`, now, statuses, excludedRunID, actorID).Scan(&count, &earliest); err != nil {
			return false, 0, translate(err)
		}
	case projectID != "":
		if err := tx.QueryRow(ctx, `SELECT COUNT(*), COALESCE(MIN(expires_at), now())
			FROM run_leases rl
			JOIN runs r ON r.id=rl.run_id
			WHERE rl.released_at IS NULL AND rl.expires_at > $1
			  AND r.status = ANY($2::text[])
			  AND rl.run_id <> $3 AND rl.project_id = $4::uuid`, now, statuses, excludedRunID, projectID).Scan(&count, &earliest); err != nil {
			return false, 0, translate(err)
		}
	default:
		if err := tx.QueryRow(ctx, `SELECT COUNT(*), COALESCE(MIN(expires_at), now())
			FROM run_leases rl
			JOIN runs r ON r.id=rl.run_id
			WHERE rl.released_at IS NULL AND rl.expires_at > $1
			  AND r.status = ANY($2::text[])
			  AND rl.run_id <> $3`, now, statuses, excludedRunID).Scan(&count, &earliest); err != nil {
			return false, 0, translate(err)
		}
	}
	if count < limit {
		return false, 0, nil
	}
	return true, normalizedRetryAfter(earliest.Sub(now)), nil
}

func (p *Postgres) runLeaseByID(ctx context.Context, tx pgx.Tx, runID string) (RunLease, bool, error) {
	var lease RunLease
	var releasedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT run_id,instance_id,actor_id,project_id,acquired_at,heartbeat_at,expires_at,released_at
		FROM run_leases WHERE run_id=$1 FOR UPDATE`, runID).
		Scan(&lease.RunID, &lease.InstanceID, &lease.ActorID, &lease.ProjectID, &lease.AcquiredAt, &lease.HeartbeatAt, &lease.ExpiresAt, &releasedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunLease{}, false, nil
	}
	if err != nil {
		return RunLease{}, false, translate(err)
	}
	lease.ReleasedAt = releasedAt
	return cloneRunLease(lease), true, nil
}

func (p *Postgres) CreateShare(ctx context.Context, share domain.ShareLink) error {
	raw, err := json.Marshal(share)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO share_links(id,project_id,flow_id,version_id,token_hash,payload,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, share.ID, share.ProjectID, share.FlowID, share.VersionID, share.TokenHash, raw, share.CreatedAt)
	return translate(err)
}

func (p *Postgres) ListShares(ctx context.Context, flowID string) ([]domain.ShareLink, error) {
	rows, err := p.pool.Query(ctx, `SELECT payload FROM share_links WHERE flow_id=$1 ORDER BY created_at DESC`, flowID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := []domain.ShareLink{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var share domain.ShareLink
		if err := json.Unmarshal(raw, &share); err != nil {
			return nil, err
		}
		result = append(result, share)
	}
	return result, rows.Err()
}

func (p *Postgres) ShareByID(ctx context.Context, id string) (domain.ShareLink, error) {
	return p.scanShare(p.pool.QueryRow(ctx, `SELECT payload FROM share_links WHERE id=$1`, id))
}

func (p *Postgres) ShareByTokenHash(ctx context.Context, hash string) (domain.ShareLink, error) {
	return p.scanShare(p.pool.QueryRow(ctx, `SELECT payload FROM share_links WHERE token_hash=$1`, hash))
}

func (p *Postgres) scanShare(row pgx.Row) (domain.ShareLink, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return domain.ShareLink{}, translate(err)
	}
	var share domain.ShareLink
	return share, json.Unmarshal(raw, &share)
}

func (p *Postgres) RevokeShare(ctx context.Context, id string) error {
	share, err := p.ShareByID(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	share.RevokedAt = &now
	raw, err := json.Marshal(share)
	if err != nil {
		return err
	}
	tag, err := p.pool.Exec(ctx, `UPDATE share_links SET payload=$2 WHERE id=$1`, id, raw)
	return translatedRows(tag.RowsAffected(), err)
}

func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func translatedRows(rows int64, err error) error {
	if err != nil {
		return translate(err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

var _ Repository = (*Postgres)(nil)
