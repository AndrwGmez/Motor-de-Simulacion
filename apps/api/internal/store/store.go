package store

import (
	"context"
	"errors"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("conflict")
	ErrPrecondition        = errors.New("precondition failed")
	ErrIdempotencyMismatch = errors.New("idempotency key was reused with a different request")
)

const RunIdempotencyRetention = 24 * time.Hour

type RunIdempotency struct {
	UserID         string
	TargetType     string
	TargetID       string
	TargetRevision string
	Key            string
	RequestHash    string
}

type Repository interface {
	RunCoordinator
	CreateUser(context.Context, domain.User) error
	UserByID(context.Context, string) (domain.User, error)
	UserByEmail(context.Context, string) (domain.User, error)
	SaveSession(context.Context, domain.Session) error
	SessionByHash(context.Context, string) (domain.Session, error)
	RevokeSession(context.Context, string) error
	RevokeSessionFamily(context.Context, string) error

	CreateProject(context.Context, domain.Project) error
	ListProjects(context.Context, string) ([]domain.Project, error)
	ProjectByID(context.Context, string) (domain.Project, error)
	UpdateProject(context.Context, domain.Project) error
	DeleteProject(context.Context, string) error
	SetProjectMember(context.Context, string, string, domain.Role) error
	RemoveProjectMember(context.Context, string, string) error
	ProjectRole(context.Context, string, string) (domain.Role, error)
	ListProjectMembers(context.Context, string) (map[string]domain.Role, error)

	CreateFlow(context.Context, domain.Flow) error
	ListFlows(context.Context, string) ([]domain.Flow, error)
	FlowByID(context.Context, string) (domain.Flow, error)
	UpdateFlow(context.Context, domain.Flow, string) error
	DeleteFlow(context.Context, string) error

	CreateVersion(context.Context, domain.FlowVersion) error
	ListVersions(context.Context, string) ([]domain.FlowVersion, error)
	VersionByID(context.Context, string) (domain.FlowVersion, error)

	CreateRun(context.Context, domain.Run, RunIdempotency) (domain.Run, bool, error)
	RunByID(context.Context, string) (domain.Run, error)
	UpdateRun(context.Context, domain.Run) error
	AppendRunEvent(context.Context, domain.Run, domain.Event) error
	ListRuns(context.Context, string) ([]domain.Run, error)
	InterruptActiveRuns(context.Context, time.Time) (int, error)

	CreateShare(context.Context, domain.ShareLink) error
	ListShares(context.Context, string) ([]domain.ShareLink, error)
	ShareByID(context.Context, string) (domain.ShareLink, error)
	ShareByTokenHash(context.Context, string) (domain.ShareLink, error)
	RevokeShare(context.Context, string) error
}

func interruptRun(run domain.Run, occurredAt time.Time) domain.Run {
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	} else {
		occurredAt = occurredAt.UTC()
	}
	var sequence, logicalTime int64
	for _, event := range run.Events {
		if event.Sequence > sequence {
			sequence = event.Sequence
		}
		if event.LogicalTimeMS > logicalTime {
			logicalTime = event.LogicalTimeMS
		}
	}
	run.Events = append(run.Events, domain.Event{
		SchemaVersion: domain.SchemaVersion,
		Type:          "run.interrupted",
		RunID:         run.ID,
		Sequence:      sequence + 1,
		OccurredAt:    occurredAt,
		LogicalTimeMS: logicalTime,
		Payload: map[string]any{
			"code":    "run.startup_recovery",
			"message": "Run interrupted because the API restarted",
		},
	})
	run.Status = "interrupted"
	run.Error = "run interrupted because the API restarted"
	run.CompletedAt = &occurredAt
	return run
}

func activeRunStatus(status string) bool {
	switch status {
	case "created", "queued", "running", "paused", "waiting":
		return true
	default:
		return false
	}
}
