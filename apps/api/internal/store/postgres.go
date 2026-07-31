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
		FROM flow_versions WHERE flow_id=$1 ORDER BY version_number`, flowID)
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
	raw, err := json.Marshal(run)
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
	if _, err = tx.Exec(ctx, `INSERT INTO runs(id,project_id,flow_id,version_id,status,payload,created_at)
		VALUES($1,$2,$3,NULLIF($4::text,'')::uuid,$5,$6,$7)`,
		run.ID, run.ProjectID, run.FlowID, run.VersionID, run.Status, raw, run.CreatedAt); err != nil {
		return domain.Run{}, false, translate(err)
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
	var raw []byte
	err := p.pool.QueryRow(ctx, `SELECT i.request_hash,r.payload
		FROM run_idempotency_keys i
		JOIN runs r ON r.id=i.run_id
		WHERE i.user_id=$1 AND i.target_type=$2 AND i.target_id=$3
		  AND i.target_revision=$4 AND i.idempotency_key=$5 AND i.expires_at > now()`,
		idempotency.UserID, idempotency.TargetType, idempotency.TargetID,
		idempotency.TargetRevision, idempotency.Key).Scan(&requestHash, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, false, nil
	}
	if err != nil {
		return domain.Run{}, false, translate(err)
	}
	if requestHash != idempotency.RequestHash {
		return domain.Run{}, false, ErrIdempotencyMismatch
	}
	var run domain.Run
	if err := json.Unmarshal(raw, &run); err != nil {
		return domain.Run{}, false, err
	}
	return run, true, nil
}

func (p *Postgres) RunByID(ctx context.Context, id string) (domain.Run, error) {
	var raw []byte
	err := p.pool.QueryRow(ctx, `SELECT payload FROM runs WHERE id=$1`, id).Scan(&raw)
	if err != nil {
		return domain.Run{}, translate(err)
	}
	var run domain.Run
	return run, json.Unmarshal(raw, &run)
}

func (p *Postgres) UpdateRun(ctx context.Context, run domain.Run) error {
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE runs SET status=$2,payload=$3,updated_at=now() WHERE id=$1`, run.ID, run.Status, raw)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	for _, event := range run.Events {
		eventRaw, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO run_events(run_id,sequence,event,occurred_at) VALUES($1,$2,$3,$4)
			ON CONFLICT(run_id,sequence) DO NOTHING`, run.ID, event.Sequence, eventRaw, event.OccurredAt); err != nil {
			return translate(err)
		}
	}
	if len(run.NodeRuns) > 0 {
		if _, err = tx.Exec(ctx, `DELETE FROM node_runs WHERE run_id=$1`, run.ID); err != nil {
			return err
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
	rows, err := p.pool.Query(ctx, `SELECT payload FROM runs WHERE flow_id=$1 ORDER BY created_at DESC`, flowID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	result := []domain.Run{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var run domain.Run
		if err := json.Unmarshal(raw, &run); err != nil {
			return nil, err
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
		run = interruptRun(run, occurredAt)
		var persistedSequence int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0) FROM run_events WHERE run_id=$1`, run.ID).Scan(&persistedSequence); err != nil {
			return 0, translate(err)
		}
		event := &run.Events[len(run.Events)-1]
		if event.Sequence <= persistedSequence {
			event.Sequence = persistedSequence + 1
		}
		runRaw, err := json.Marshal(run)
		if err != nil {
			return 0, err
		}
		eventRaw, err := json.Marshal(event)
		if err != nil {
			return 0, err
		}
		tag, err := tx.Exec(ctx, `UPDATE runs SET status='interrupted',payload=$2,updated_at=now()
			WHERE id=$1 AND status = ANY($3::text[])`,
			run.ID, runRaw, []string{"created", "queued", "running", "paused", "waiting"})
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
