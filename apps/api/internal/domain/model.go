package domain

import (
	"encoding/json"
	"time"
)

const (
	SchemaVersion = "1.0"
	MaxNodes      = 5000
	MaxEdges      = 10000
)

type NodeType string

const (
	NodeTrigger     NodeType = "trigger"
	NodeProcess     NodeType = "process"
	NodeDecision    NodeType = "decision"
	NodeData        NodeType = "data"
	NodeIntegration NodeType = "integration"
	NodeDelay       NodeType = "delay"
	NodeEnd         NodeType = "end"
	NodeGroup       NodeType = "group"
)

func (t NodeType) Valid() bool {
	switch t {
	case NodeTrigger, NodeProcess, NodeDecision, NodeData, NodeIntegration, NodeDelay, NodeEnd, NodeGroup:
		return true
	default:
		return false
	}
}

type ActivationMode string

const (
	ActivationEach ActivationMode = "each"
	ActivationAny  ActivationMode = "any"
	ActivationAll  ActivationMode = "all"
)

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type Port struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type Ports struct {
	Inputs  []Port `json:"inputs,omitempty"`
	Outputs []Port `json:"outputs,omitempty"`
}

type Node struct {
	ID             string         `json:"id"`
	Type           NodeType       `json:"type"`
	Label          string         `json:"label"`
	Description    string         `json:"description,omitempty"`
	Inputs         []Port         `json:"inputs"`
	Outputs        []Port         `json:"outputs"`
	ActivationMode ActivationMode `json:"activationMode"`
	DurationMS     int64          `json:"durationMs"`
	Configuration  map[string]any `json:"configuration"`
	Position       Position       `json:"position"`
	Locked         bool           `json:"locked"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

func (n Node) EffectiveActivationMode() ActivationMode {
	if n.ActivationMode == "" {
		return ActivationEach
	}
	return n.ActivationMode
}

type Condition struct {
	Field      string      `json:"field,omitempty"`
	Operator   string      `json:"operator,omitempty"`
	Value      any         `json:"value,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
	And        []Condition `json:"and,omitempty"`
	Or         []Condition `json:"or,omitempty"`
}

type Edge struct {
	ID         string     `json:"id"`
	Source     string     `json:"source"`
	Target     string     `json:"target"`
	SourcePort string     `json:"sourcePort,omitempty"`
	TargetPort string     `json:"targetPort,omitempty"`
	Label      string     `json:"label,omitempty"`
	Condition  *Condition `json:"condition,omitempty"`
	Priority   int        `json:"priority"`
	Default    bool       `json:"isDefault"`
}

type VariableDefinition struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

type Camera struct {
	Position Position `json:"position"`
	Target   Position `json:"target"`
}

type Layout struct {
	Mode      string  `json:"mode"`
	ClusterBy string  `json:"clusterBy,omitempty"`
	Camera    *Camera `json:"camera,omitempty"`
}

type FlowDefinition struct {
	SchemaVersion string               `json:"schemaVersion"`
	Name          string               `json:"name"`
	Description   string               `json:"description,omitempty"`
	Variables     []VariableDefinition `json:"variables"`
	Layout        Layout               `json:"layout"`
	Nodes         []Node               `json:"nodes"`
	Edges         []Edge               `json:"edges"`
	Metadata      map[string]any       `json:"metadata,omitempty"`
}

// Normalize returns the canonical wire representation described by
// packages/contracts/schemas/flow-definition.schema.json. It also upgrades the
// intentionally small set of defaults accepted by the editor and parser.
func (f FlowDefinition) Normalize() FlowDefinition {
	result := f.Clone()
	if result.SchemaVersion == "" {
		result.SchemaVersion = SchemaVersion
	}
	if result.Variables == nil {
		result.Variables = []VariableDefinition{}
	}
	if result.Nodes == nil {
		result.Nodes = []Node{}
	}
	if result.Edges == nil {
		result.Edges = []Edge{}
	}
	if result.Layout.Mode == "" {
		result.Layout.Mode = "force"
	}
	for index := range result.Nodes {
		node := &result.Nodes[index]
		if node.Inputs == nil {
			node.Inputs = []Port{}
		}
		if node.Outputs == nil {
			node.Outputs = []Port{}
		}
		if node.ActivationMode == "" {
			node.ActivationMode = ActivationEach
		}
		if node.Configuration == nil {
			node.Configuration = map[string]any{}
		}
	}
	return result
}

func (f FlowDefinition) Clone() FlowDefinition {
	raw, _ := json.Marshal(f)
	var clone FlowDefinition
	_ = json.Unmarshal(raw, &clone)
	return clone
}

type IssueSeverity string

const (
	SeverityInfo    IssueSeverity = "info"
	SeverityWarning IssueSeverity = "warning"
	SeverityError   IssueSeverity = "error"
)

type ValidationIssue struct {
	Code     string        `json:"code"`
	Message  string        `json:"message"`
	Severity IssueSeverity `json:"severity"`
	Path     string        `json:"path,omitempty"`
	NodeID   string        `json:"nodeId,omitempty"`
	EdgeID   string        `json:"edgeId,omitempty"`
}

type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Issues []ValidationIssue `json:"issues"`
}

type Cycle struct {
	NodeIDs []string `json:"nodeIds"`
	HasExit bool     `json:"hasExit"`
}

type Analysis struct {
	NodeCount            int      `json:"nodeCount"`
	EdgeCount            int      `json:"edgeCount"`
	TriggerCount         int      `json:"triggerCount"`
	EndCount             int      `json:"endCount"`
	MaxDepth             int      `json:"maxDepth"`
	CyclomaticComplexity int      `json:"cyclomaticComplexity"`
	UnreachableNodeIDs   []string `json:"unreachableNodeIds"`
	DisconnectedNodeIDs  []string `json:"disconnectedNodeIds"`
	Cycles               []Cycle  `json:"cycles"`
	PathCount            int      `json:"pathCount"`
	PathsTruncated       bool     `json:"pathsTruncated"`
	CriticalPathNodeIDs  []string `json:"criticalPathNodeIds,omitempty"`
	CriticalPathMS       int64    `json:"criticalPathMs,omitempty"`
	CriticalPathApplies  bool     `json:"criticalPathApplies"`
	BottleneckNodeIDs    []string `json:"bottleneckNodeIds,omitempty"`
}

type Event struct {
	SchemaVersion string         `json:"schemaVersion"`
	Type          string         `json:"type"`
	RunID         string         `json:"runId"`
	Sequence      int64          `json:"sequence"`
	OccurredAt    time.Time      `json:"occurredAt"`
	LogicalTimeMS int64          `json:"logicalTimeMs"`
	Payload       map[string]any `json:"payload,omitempty"`
}

type NodeRun struct {
	NodeID      string         `json:"nodeId"`
	TokenID     string         `json:"tokenId"`
	Status      string         `json:"status"`
	StartedMS   int64          `json:"startedMs"`
	CompletedMS int64          `json:"completedMs"`
	Output      map[string]any `json:"output,omitempty"`
	Error       string         `json:"error,omitempty"`
}

type Run struct {
	ID             string         `json:"id"`
	TraceID        string         `json:"traceId,omitempty"`
	ProjectID      string         `json:"projectId"`
	FlowID         string         `json:"flowId"`
	VersionID      string         `json:"versionId,omitempty"`
	Status         string         `json:"status"`
	Input          map[string]any `json:"input,omitempty"`
	Output         map[string]any `json:"output,omitempty"`
	TriggerID      string         `json:"triggerId"`
	Definition     FlowDefinition `json:"definition,omitempty"`
	Events         []Event        `json:"events,omitempty"`
	NodeRuns       []NodeRun      `json:"nodeRuns,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	StartedAt      *time.Time     `json:"startedAt,omitempty"`
	CompletedAt    *time.Time     `json:"completedAt,omitempty"`
	Error          string         `json:"error,omitempty"`
	DefinitionETag string         `json:"definitionEtag,omitempty"`
}

type Role string

const (
	RoleOwner  Role = "owner"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

func (r Role) Allows(required Role) bool {
	rank := map[Role]int{RoleViewer: 1, RoleEditor: 2, RoleOwner: 3}
	return rank[r] >= rank[required]
}

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"displayName"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Session struct {
	TokenHash string
	UserID    string
	Kind      string
	ExpiresAt time.Time
	RevokedAt *time.Time
	FamilyID  string
}

type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	OwnerID     string    `json:"ownerId"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Flow struct {
	ID          string         `json:"id"`
	ProjectID   string         `json:"projectId"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Draft       FlowDefinition `json:"draft"`
	DraftETag   string         `json:"draftEtag"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

type FlowVersion struct {
	ID          string         `json:"id"`
	FlowID      string         `json:"flowId"`
	Number      int            `json:"number"`
	Definition  FlowDefinition `json:"definition,omitempty"`
	Checksum    string         `json:"checksum"`
	CreatedAt   time.Time      `json:"publishedAt"`
	PublishedBy string         `json:"publishedBy"`
}

type ShareLink struct {
	ID        string     `json:"id"`
	ProjectID string     `json:"projectId"`
	FlowID    string     `json:"flowId"`
	VersionID string     `json:"versionId"`
	RunIDs    []string   `json:"runIds,omitempty"`
	TokenHash string     `json:"-"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}
