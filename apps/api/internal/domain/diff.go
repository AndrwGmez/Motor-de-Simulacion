package domain

const (
	FlowDiffSchemaVersion    = "1.0"
	FlowDiffAlgorithmVersion = "semantic-v1"
)

type DiffRevisionKind string

const (
	DiffRevisionDraft   DiffRevisionKind = "draft"
	DiffRevisionVersion DiffRevisionKind = "version"
)

type DiffImpact string

const (
	DiffImpactNone       DiffImpact = "none"
	DiffImpactVisual     DiffImpact = "visual"
	DiffImpactBehavioral DiffImpact = "behavioral"
	DiffImpactBreaking   DiffImpact = "breaking"
)

type DiffOperation string

const (
	DiffOperationAdded    DiffOperation = "added"
	DiffOperationRemoved  DiffOperation = "removed"
	DiffOperationModified DiffOperation = "modified"
)

type DiffEntityType string

const (
	DiffEntityFlow     DiffEntityType = "flow"
	DiffEntityLayout   DiffEntityType = "layout"
	DiffEntityVariable DiffEntityType = "variable"
	DiffEntityNode     DiffEntityType = "node"
	DiffEntityEdge     DiffEntityType = "edge"
)

// FlowRevisionRef identifies one side of a comparison. Draft references omit
// versionId and versionNumber; published references include both.
type FlowRevisionRef struct {
	Kind          DiffRevisionKind `json:"kind"`
	Checksum      string           `json:"checksum"`
	VersionID     string           `json:"versionId,omitempty"`
	VersionNumber int              `json:"versionNumber,omitempty"`
}

// DiffValue keeps absence distinct from a JSON null value. This matters for
// optional fields and for the missing side of added or removed entities.
type DiffValue struct {
	Exists bool `json:"exists"`
	Value  any  `json:"value"`
}

type FieldDelta struct {
	Path   string     `json:"path"`
	Impact DiffImpact `json:"impact"`
	Before DiffValue  `json:"before"`
	After  DiffValue  `json:"after"`
}

type SemanticChange struct {
	Code       string         `json:"code"`
	EntityType DiffEntityType `json:"entityType"`
	EntityID   string         `json:"entityId,omitempty"`
	Operation  DiffOperation  `json:"operation"`
	Impact     DiffImpact     `json:"impact"`
	Before     *DiffValue     `json:"before,omitempty"`
	After      *DiffValue     `json:"after,omitempty"`
	Fields     []FieldDelta   `json:"fields"`
}

type DiffCountsByOperation struct {
	Added    int `json:"added"`
	Removed  int `json:"removed"`
	Modified int `json:"modified"`
}

type DiffCountsByImpact struct {
	Visual     int `json:"visual"`
	Behavioral int `json:"behavioral"`
	Breaking   int `json:"breaking"`
}

type DiffCountsByEntity struct {
	Flow     int `json:"flow"`
	Layout   int `json:"layout"`
	Variable int `json:"variable"`
	Node     int `json:"node"`
	Edge     int `json:"edge"`
}

type FlowDiffSummary struct {
	ExactMatch             bool                  `json:"exactMatch"`
	SemanticMatch          bool                  `json:"semanticMatch"`
	BehaviorallyEquivalent bool                  `json:"behaviorallyEquivalent"`
	OverallImpact          DiffImpact            `json:"overallImpact"`
	ChangeCount            int                   `json:"changeCount"`
	FieldChangeCount       int                   `json:"fieldChangeCount"`
	ByOperation            DiffCountsByOperation `json:"byOperation"`
	ByImpact               DiffCountsByImpact    `json:"byImpact"`
	ByEntity               DiffCountsByEntity    `json:"byEntity"`
}

type FlowDiff struct {
	SchemaVersion    string           `json:"schemaVersion"`
	AlgorithmVersion string           `json:"algorithmVersion"`
	FlowID           string           `json:"flowId"`
	Base             FlowRevisionRef  `json:"base"`
	Target           FlowRevisionRef  `json:"target"`
	Summary          FlowDiffSummary  `json:"summary"`
	Changes          []SemanticChange `json:"changes"`
}
