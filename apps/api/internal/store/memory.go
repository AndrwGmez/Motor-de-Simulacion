package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
)

type Memory struct {
	mu  sync.RWMutex
	now func() time.Time

	users        map[string]domain.User
	usersByMail  map[string]string
	sessions     map[string]domain.Session
	projects     map[string]domain.Project
	members      map[string]map[string]domain.Role
	flows        map[string]domain.Flow
	versions     map[string]domain.FlowVersion
	runs         map[string]domain.Run
	runLeases    map[string]RunLease
	idempotency  map[string]memoryRunIdempotency
	shares       map[string]domain.ShareLink
	sharesByHash map[string]string
	enterprise   enterpriseMemoryState
}

type memoryRunIdempotency struct {
	RequestHash string
	RunID       string
	ExpiresAt   time.Time
}

func NewMemory() *Memory {
	return &Memory{
		now:   time.Now,
		users: map[string]domain.User{}, usersByMail: map[string]string{},
		sessions: map[string]domain.Session{}, projects: map[string]domain.Project{},
		members: map[string]map[string]domain.Role{}, flows: map[string]domain.Flow{},
		versions: map[string]domain.FlowVersion{}, runs: map[string]domain.Run{},
		runLeases:   map[string]RunLease{},
		idempotency: map[string]memoryRunIdempotency{}, shares: map[string]domain.ShareLink{},
		sharesByHash: map[string]string{},
		enterprise:   newEnterpriseMemoryState(),
	}
}

func (m *Memory) CreateUser(_ context.Context, user domain.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	email := strings.ToLower(strings.TrimSpace(user.Email))
	if _, exists := m.usersByMail[email]; exists {
		return ErrConflict
	}
	user.Email = email
	m.users[user.ID] = clone(user)
	m.usersByMail[email] = user.ID
	return nil
}

func (m *Memory) UserByID(_ context.Context, id string) (domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	user, ok := m.users[id]
	if !ok {
		return domain.User{}, ErrNotFound
	}
	return clone(user), nil
}

func (m *Memory) UserByEmail(_ context.Context, email string) (domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.usersByMail[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return domain.User{}, ErrNotFound
	}
	return clone(m.users[id]), nil
}

func (m *Memory) SaveSession(_ context.Context, session domain.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.TokenHash] = session
	return nil
}

func (m *Memory) SessionByHash(_ context.Context, hash string) (domain.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[hash]
	if !ok {
		return domain.Session{}, ErrNotFound
	}
	return session, nil
}

func (m *Memory) RevokeSession(_ context.Context, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[hash]
	if !ok {
		return ErrNotFound
	}
	now := nowUTC()
	session.RevokedAt = &now
	m.sessions[hash] = session
	return nil
}

func (m *Memory) RevokeSessionFamily(_ context.Context, family string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := nowUTC()
	for hash, session := range m.sessions {
		if session.FamilyID == family {
			session.RevokedAt = &now
			m.sessions[hash] = session
		}
	}
	return nil
}

func (m *Memory) CreateProject(_ context.Context, project domain.Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.projects[project.ID]; exists {
		return ErrConflict
	}
	m.projects[project.ID] = clone(project)
	m.members[project.ID] = map[string]domain.Role{project.OwnerID: domain.RoleOwner}
	return nil
}

func (m *Memory) ListProjects(_ context.Context, userID string) ([]domain.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := []domain.Project{}
	for projectID, roles := range m.members {
		if _, member := roles[userID]; member {
			result = append(result, clone(m.projects[projectID]))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

func (m *Memory) ProjectByID(_ context.Context, id string) (domain.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	project, ok := m.projects[id]
	if !ok {
		return domain.Project{}, ErrNotFound
	}
	return clone(project), nil
}

func (m *Memory) UpdateProject(_ context.Context, project domain.Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[project.ID]; !ok {
		return ErrNotFound
	}
	m.projects[project.ID] = clone(project)
	return nil
}

func (m *Memory) DeleteProject(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[id]; !ok {
		return ErrNotFound
	}
	delete(m.projects, id)
	delete(m.members, id)
	for flowID, flow := range m.flows {
		if flow.ProjectID == id {
			delete(m.flows, flowID)
		}
	}
	return nil
}

func (m *Memory) SetProjectMember(_ context.Context, projectID, userID string, role domain.Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.projects[projectID]; !ok {
		return ErrNotFound
	}
	if _, ok := m.users[userID]; !ok {
		return ErrNotFound
	}
	if m.members[projectID] == nil {
		m.members[projectID] = map[string]domain.Role{}
	}
	m.members[projectID][userID] = role
	return nil
}

func (m *Memory) RemoveProjectMember(_ context.Context, projectID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.members[projectID][userID]; !ok {
		return ErrNotFound
	}
	delete(m.members[projectID], userID)
	return nil
}

func (m *Memory) ProjectRole(_ context.Context, projectID, userID string) (domain.Role, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	role, ok := m.members[projectID][userID]
	if !ok {
		return "", ErrNotFound
	}
	return role, nil
}

func (m *Memory) ListProjectMembers(_ context.Context, projectID string) (map[string]domain.Role, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	roles, ok := m.members[projectID]
	if !ok {
		return nil, ErrNotFound
	}
	result := map[string]domain.Role{}
	for id, role := range roles {
		result[id] = role
	}
	return result, nil
}

func (m *Memory) CreateFlow(_ context.Context, flow domain.Flow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.flows[flow.ID]; exists {
		return ErrConflict
	}
	if _, exists := m.projects[flow.ProjectID]; !exists {
		return ErrNotFound
	}
	m.flows[flow.ID] = clone(flow)
	return nil
}

func (m *Memory) ListFlows(_ context.Context, projectID string) ([]domain.Flow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := []domain.Flow{}
	for _, flow := range m.flows {
		if flow.ProjectID == projectID {
			result = append(result, clone(flow))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

func (m *Memory) FlowByID(_ context.Context, id string) (domain.Flow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	flow, ok := m.flows[id]
	if !ok {
		return domain.Flow{}, ErrNotFound
	}
	return clone(flow), nil
}

func (m *Memory) UpdateFlow(_ context.Context, flow domain.Flow, expectedETag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.flows[flow.ID]
	if !ok {
		return ErrNotFound
	}
	if expectedETag == "" || expectedETag != current.DraftETag {
		return ErrPrecondition
	}
	m.flows[flow.ID] = clone(flow)
	return nil
}

func (m *Memory) DeleteFlow(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.flows[id]; !ok {
		return ErrNotFound
	}
	delete(m.flows, id)
	return nil
}

func (m *Memory) CreateVersion(_ context.Context, version domain.FlowVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.versions[version.ID]; exists {
		return ErrConflict
	}
	m.versions[version.ID] = clone(version)
	return nil
}

func (m *Memory) ListVersions(_ context.Context, flowID string) ([]domain.FlowVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := []domain.FlowVersion{}
	for _, version := range m.versions {
		if version.FlowID == flowID {
			result = append(result, clone(version))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Number > result[j].Number })
	return result, nil
}

func (m *Memory) VersionByID(_ context.Context, id string) (domain.FlowVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	version, ok := m.versions[id]
	if !ok {
		return domain.FlowVersion{}, ErrNotFound
	}
	return clone(version), nil
}

func (m *Memory) CreateRun(_ context.Context, run domain.Run, idempotency RunIdempotency) (domain.Run, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idempotency.Key != "" {
		key := memoryIdempotencyKey(idempotency)
		if existing, exists := m.idempotency[key]; exists {
			if existing.ExpiresAt.After(m.now().UTC()) {
				if existing.RequestHash != idempotency.RequestHash {
					return domain.Run{}, false, ErrIdempotencyMismatch
				}
				stored, ok := m.runs[existing.RunID]
				if !ok {
					return domain.Run{}, false, ErrNotFound
				}
				return clone(stored), false, nil
			}
			delete(m.idempotency, key)
		}
	}
	if _, exists := m.runs[run.ID]; exists {
		return domain.Run{}, false, ErrConflict
	}
	m.runs[run.ID] = clone(run)
	if activeRunStatus(run.Status) {
		leaseTime := run.CreatedAt.UTC()
		if leaseTime.IsZero() {
			leaseTime = m.now().UTC()
		}
		m.runLeases[run.ID] = RunLease{
			RunID: run.ID, InstanceID: "unassigned", ActorID: idempotency.UserID,
			ProjectID: run.ProjectID, AcquiredAt: leaseTime, HeartbeatAt: leaseTime, ExpiresAt: leaseTime,
		}
	}
	if idempotency.Key != "" {
		m.idempotency[memoryIdempotencyKey(idempotency)] = memoryRunIdempotency{
			RequestHash: idempotency.RequestHash,
			RunID:       run.ID,
			ExpiresAt:   m.now().UTC().Add(RunIdempotencyRetention),
		}
	}
	return clone(run), true, nil
}

func (m *Memory) RunByID(_ context.Context, id string) (domain.Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[id]
	if !ok {
		return domain.Run{}, ErrNotFound
	}
	return clone(run), nil
}

func (m *Memory) UpdateRun(_ context.Context, run domain.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.ID]; !ok {
		return ErrNotFound
	}
	m.runs[run.ID] = clone(run)
	return nil
}

// AppendRunEvent persists the mutable run state and exactly one new event.
// Keeping this operation separate from UpdateRun prevents every event from
// copying the complete history as a run grows.
func (m *Memory) AppendRunEvent(_ context.Context, run domain.Run, event domain.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appendRunEventLocked(run, event)
}

func (m *Memory) appendRunEventLocked(run domain.Run, event domain.Event) error {
	stored, ok := m.runs[run.ID]
	if !ok {
		return ErrNotFound
	}
	if !activeRunStatus(stored.Status) {
		return ErrConflict
	}
	expectedSequence := int64(1)
	if count := len(stored.Events); count > 0 {
		expectedSequence = stored.Events[count-1].Sequence + 1
	}
	if event.Sequence != expectedSequence {
		return ErrConflict
	}
	stored.Status = run.Status
	stored.Output = clone(run.Output)
	stored.StartedAt = clone(run.StartedAt)
	stored.CompletedAt = clone(run.CompletedAt)
	stored.Error = run.Error
	if len(run.NodeRuns) > 0 {
		stored.NodeRuns = clone(run.NodeRuns)
	}
	stored.Events = append(stored.Events, clone(event))
	m.runs[run.ID] = stored
	return nil
}

func (m *Memory) ListRuns(_ context.Context, flowID string) ([]domain.Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := []domain.Run{}
	for _, run := range m.runs {
		if run.FlowID == flowID {
			result = append(result, clone(run))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (m *Memory) InterruptActiveRuns(_ context.Context, occurredAt time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	interrupted := 0
	for id, run := range m.runs {
		if !activeRunStatus(run.Status) {
			continue
		}
		m.runs[id] = clone(interruptRun(run, occurredAt))
		interrupted++
	}
	return interrupted, nil
}

func memoryIdempotencyKey(idempotency RunIdempotency) string {
	return idempotency.UserID + "\x00" +
		idempotency.TargetType + "\x00" +
		idempotency.TargetID + "\x00" +
		idempotency.TargetRevision + "\x00" +
		idempotency.Key
}

func (m *Memory) CreateShare(_ context.Context, share domain.ShareLink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.shares[share.ID]; exists {
		return ErrConflict
	}
	m.shares[share.ID] = clone(share)
	m.sharesByHash[share.TokenHash] = share.ID
	return nil
}

func (m *Memory) ListShares(_ context.Context, flowID string) ([]domain.ShareLink, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := []domain.ShareLink{}
	for _, share := range m.shares {
		if share.FlowID == flowID {
			result = append(result, clone(share))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (m *Memory) ShareByID(_ context.Context, id string) (domain.ShareLink, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	share, ok := m.shares[id]
	if !ok {
		return domain.ShareLink{}, ErrNotFound
	}
	return clone(share), nil
}

func (m *Memory) ShareByTokenHash(_ context.Context, hash string) (domain.ShareLink, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.sharesByHash[hash]
	if !ok {
		return domain.ShareLink{}, ErrNotFound
	}
	return clone(m.shares[id]), nil
}

func (m *Memory) RevokeShare(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	share, ok := m.shares[id]
	if !ok {
		return ErrNotFound
	}
	now := nowUTC()
	share.RevokedAt = &now
	m.shares[id] = share
	return nil
}

func clone[T any](value T) T {
	raw, _ := json.Marshal(value)
	var result T
	_ = json.Unmarshal(raw, &result)
	return result
}

func nowUTC() time.Time { return time.Now().UTC() }
