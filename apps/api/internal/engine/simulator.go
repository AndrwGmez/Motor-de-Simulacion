package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
)

type RunOverrides struct {
	ForcedEdges map[string]string `json:"forcedEdges,omitempty"`
	FailedNodes map[string]string `json:"failedNodes,omitempty"`
}

const (
	SimulationDefaultMaxSteps         = 10_000
	SimulationMaxSteps                = 100_000
	SimulationDefaultMaxVisitsPerNode = 100
	SimulationMaxVisitsPerNode        = 10_000
	SimulationMaxInputProperties      = 250
	SimulationMaxOverrides            = 100
)

type RunOptions struct {
	RunID            string         `json:"runId,omitempty"`
	TriggerID        string         `json:"triggerId,omitempty"`
	Input            map[string]any `json:"input,omitempty"`
	Overrides        RunOverrides   `json:"overrides,omitempty"`
	MaxSteps         int            `json:"maxSteps,omitempty"`
	MaxVisitsPerNode int            `json:"maxVisitsPerNode,omitempty"`
	StartedAt        time.Time      `json:"-"`
}

type SimulationResult struct {
	RunID       string           `json:"runId"`
	Status      string           `json:"status"`
	Output      map[string]any   `json:"output,omitempty"`
	Events      []domain.Event   `json:"events"`
	NodeRuns    []domain.NodeRun `json:"nodeRuns"`
	VisitedPath []string         `json:"visitedPath"`
	EdgeCounts  map[string]int   `json:"edgeCounts"`
	NodeTimesMS map[string]int64 `json:"nodeTimesMs"`
	Error       string           `json:"error,omitempty"`
}

// forkFrame correlates siblings created by one concrete fork visit. Fork IDs are
// unique within a run, so iterations and nested forks never share a barrier.
type forkFrame struct {
	ID        string
	BranchID  string
	Expected  []string
	Iteration int
}

type token struct {
	ID         string
	NodeID     string
	SourceEdge string
	Context    map[string]any
	Writes     map[string]writeValue
	Lineage    []forkFrame
	LogicalMS  int64
}

type writeValue struct {
	Value   any
	Deleted bool
}

type barrierKey struct {
	NodeID    string
	ForkID    string
	Iteration int
}

type joinBarrier struct {
	Key       barrierKey
	Expected  []string
	Arrivals  map[string]*token
	LogicalMS int64
}

type Simulator struct{}

func NewSimulator() *Simulator { return &Simulator{} }

func (s *Simulator) Run(flow domain.FlowDefinition, options RunOptions) (SimulationResult, error) {
	if err := validateRunOptions(options); err != nil {
		return SimulationResult{}, err
	}
	validation := Validate(flow)
	if !validation.Valid {
		return SimulationResult{}, fmt.Errorf("flow is invalid")
	}
	normalizeRunOptions(&options)

	nodes := make(map[string]domain.Node, len(flow.Nodes))
	outgoing := map[string][]domain.Edge{}
	triggers := []string{}
	for _, node := range flow.Nodes {
		nodes[node.ID] = node
		if node.Type == domain.NodeTrigger {
			triggers = append(triggers, node.ID)
		}
	}
	for _, edge := range flow.Edges {
		outgoing[edge.Source] = append(outgoing[edge.Source], edge)
	}
	for id := range outgoing {
		sort.Slice(outgoing[id], func(i, j int) bool {
			if outgoing[id][i].Priority != outgoing[id][j].Priority {
				return outgoing[id][i].Priority < outgoing[id][j].Priority
			}
			return outgoing[id][i].ID < outgoing[id][j].ID
		})
	}
	sort.Strings(triggers)
	triggerID := options.TriggerID
	if triggerID == "" && len(triggers) == 1 {
		triggerID = triggers[0]
	}
	if nodes[triggerID].Type != domain.NodeTrigger {
		return SimulationResult{}, errors.New("a valid triggerId must be selected")
	}

	result := SimulationResult{
		RunID: options.RunID, Status: "running", Events: []domain.Event{},
		NodeRuns: []domain.NodeRun{}, VisitedPath: []string{}, EdgeCounts: map[string]int{},
		NodeTimesMS: map[string]int64{},
	}
	sequence, nextToken, nextFork := int64(0), 0, 0
	emit := func(eventType string, logicalMS int64, payload map[string]any) {
		sequence++
		result.Events = append(result.Events, domain.Event{
			SchemaVersion: domain.SchemaVersion,
			Type:          eventType,
			RunID:         options.RunID,
			Sequence:      sequence,
			OccurredAt:    options.StartedAt.Add(time.Duration(logicalMS) * time.Millisecond),
			LogicalTimeMS: logicalMS,
			Payload:       payload,
		})
	}

	queue := []*token{}
	enqueue := func(nodeID, sourceEdge string, data map[string]any, writes map[string]writeValue, lineage []forkFrame, logicalMS int64) {
		nextToken++
		item := &token{
			ID:         fmt.Sprintf("token-%06d", nextToken),
			NodeID:     nodeID,
			SourceEdge: sourceEdge,
			Context:    cloneMap(data),
			Writes:     cloneWrites(writes),
			Lineage:    cloneLineage(lineage),
			LogicalMS:  logicalMS,
		}
		queue = append(queue, item)
		emit("node.queued", logicalMS, map[string]any{"nodeId": nodeID, "tokenId": item.ID})
	}

	context := withVariableDefaults(flow, options.Input)
	emit("run.started", 0, map[string]any{"triggerId": triggerID})
	enqueue(triggerID, "", context, map[string]writeValue{}, nil, 0)

	visits := map[string]int{}
	joinBarriers := map[barrierKey]*joinBarrier{}
	anyResolved := map[barrierKey]bool{}
	steps := 0
	failed := false
	limitExceeded := false
	runError, runCode := "", ""
	outputs := []map[string]any{}

	markFailure := func(code, message string) {
		failed = true
		if runError == "" {
			runError, runCode = message, code
		}
	}

	for len(queue) > 0 {
		sort.SliceStable(queue, func(i, j int) bool {
			if queue[i].LogicalMS != queue[j].LogicalMS {
				return queue[i].LogicalMS < queue[j].LogicalMS
			}
			if queue[i].NodeID != queue[j].NodeID {
				return queue[i].NodeID < queue[j].NodeID
			}
			return queue[i].ID < queue[j].ID
		})
		current := queue[0]
		queue = queue[1:]
		node := nodes[current.NodeID]

		switch node.EffectiveActivationMode() {
		case domain.ActivationAny:
			frame, ok := innermostFork(current.Lineage)
			if !ok {
				message := fmt.Sprintf("join %q received an uncorrelated token", node.ID)
				markFailure("join.uncorrelated", message)
				emit("node.failed", current.LogicalMS, map[string]any{
					"nodeId": node.ID, "tokenId": current.ID, "code": "join.uncorrelated", "error": message,
				})
				result.NodeRuns = append(result.NodeRuns, failedNodeRun(node.ID, current, message))
				continue
			}
			key := barrierKey{NodeID: node.ID, ForkID: frame.ID, Iteration: frame.Iteration}
			if anyResolved[key] {
				emit("node.skipped", current.LogicalMS, map[string]any{
					"nodeId": node.ID, "tokenId": current.ID, "forkId": frame.ID,
					"reason": "join.any_already_resolved",
				})
				result.NodeRuns = append(result.NodeRuns, domain.NodeRun{
					NodeID: node.ID, TokenID: current.ID, Status: "skipped",
					StartedMS: current.LogicalMS, CompletedMS: current.LogicalMS,
				})
				continue
			}
			anyResolved[key] = true
			current.Lineage = popLineage(current.Lineage)

		case domain.ActivationAll:
			frame, ok := innermostFork(current.Lineage)
			if !ok {
				message := fmt.Sprintf("join %q received an uncorrelated token", node.ID)
				markFailure("join.uncorrelated", message)
				emit("node.failed", current.LogicalMS, map[string]any{
					"nodeId": node.ID, "tokenId": current.ID, "code": "join.uncorrelated", "error": message,
				})
				result.NodeRuns = append(result.NodeRuns, failedNodeRun(node.ID, current, message))
				continue
			}
			key := barrierKey{NodeID: node.ID, ForkID: frame.ID, Iteration: frame.Iteration}
			barrier := joinBarriers[key]
			if barrier == nil {
				barrier = &joinBarrier{
					Key: key, Expected: append([]string(nil), frame.Expected...),
					Arrivals: map[string]*token{},
				}
				joinBarriers[key] = barrier
			}
			if _, duplicate := barrier.Arrivals[frame.BranchID]; duplicate {
				emit("node.skipped", current.LogicalMS, map[string]any{
					"nodeId": node.ID, "tokenId": current.ID, "forkId": frame.ID,
					"branchId": frame.BranchID, "reason": "join.all_duplicate_branch",
				})
				result.NodeRuns = append(result.NodeRuns, domain.NodeRun{
					NodeID: node.ID, TokenID: current.ID, Status: "skipped",
					StartedMS: current.LogicalMS, CompletedMS: current.LogicalMS,
				})
				continue
			}
			barrier.Arrivals[frame.BranchID] = current
			if current.LogicalMS > barrier.LogicalMS {
				barrier.LogicalMS = current.LogicalMS
			}
			if !barrierComplete(barrier) {
				emit("node.waiting", current.LogicalMS, map[string]any{
					"nodeId": node.ID, "tokenId": current.ID, "forkId": frame.ID,
					"branchId": frame.BranchID, "received": len(barrier.Arrivals),
					"expected": len(barrier.Expected),
				})
				result.NodeRuns = append(result.NodeRuns, domain.NodeRun{
					NodeID: node.ID, TokenID: current.ID, Status: "waiting",
					StartedMS: current.LogicalMS, CompletedMS: current.LogicalMS,
				})
				continue
			}

			arrivals := orderedArrivals(barrier)
			delete(joinBarriers, key)
			mergedContext, mergedWrites, mergeErr := mergeTokens(
				options.Input,
				withVariableDefaults(flow, nil),
				arrivals,
			)
			if mergeErr != nil {
				message := mergeErr.Error()
				markFailure("context.merge_conflict", message)
				emit("node.failed", barrier.LogicalMS, map[string]any{
					"nodeId": node.ID, "tokenId": current.ID,
					"forkId": frame.ID, "code": "context.merge_conflict", "error": message,
				})
				current.LogicalMS = barrier.LogicalMS
				result.NodeRuns = append(result.NodeRuns, failedNodeRun(node.ID, current, message))
				continue
			}
			current.Context = mergedContext
			current.Writes = mergedWrites
			current.Lineage = popLineage(current.Lineage)
			current.LogicalMS = barrier.LogicalMS
		}

		nextStep := steps + 1
		nextVisit := visits[node.ID] + 1
		if nextStep > options.MaxSteps {
			message := fmt.Sprintf("maximum step count %d exceeded", options.MaxSteps)
			runError, runCode = message, "run.max_steps"
			failed, limitExceeded = true, true
			emit("run.limit_exceeded", current.LogicalMS, map[string]any{
				"code": "run.max_steps", "limit": "maxSteps", "maximum": options.MaxSteps,
				"actual": nextStep, "nodeId": node.ID,
			})
			queue = nil
			break
		}
		if nextVisit > options.MaxVisitsPerNode {
			message := fmt.Sprintf("maximum visits per node %d exceeded for %q", options.MaxVisitsPerNode, node.ID)
			runError, runCode = message, "run.max_visits_per_node"
			failed, limitExceeded = true, true
			emit("run.limit_exceeded", current.LogicalMS, map[string]any{
				"code": "run.max_visits_per_node", "limit": "maxVisitsPerNode",
				"maximum": options.MaxVisitsPerNode, "actual": nextVisit, "nodeId": node.ID,
			})
			queue = nil
			break
		}
		steps, visits[node.ID] = nextStep, nextVisit

		result.VisitedPath = append(result.VisitedPath, node.ID)
		started := current.LogicalMS
		emit("node.started", started, map[string]any{"nodeId": node.ID, "tokenId": current.ID})

		if message, forceFailure := options.Overrides.FailedNodes[node.ID]; forceFailure {
			if message == "" {
				message = "forced node failure"
			}
			markFailure("node.forced_failure", message)
			emit("node.failed", started, map[string]any{
				"nodeId": node.ID, "tokenId": current.ID,
				"code": "node.forced_failure", "error": message,
			})
			result.NodeRuns = append(result.NodeRuns, failedNodeRun(node.ID, current, message))
			continue
		}
		if shouldFail(node) {
			code := configString(node.Configuration, "errorCode", "integration.simulated_failure")
			message := fmt.Sprintf("simulated integration failure (%s)", code)
			markFailure("integration.simulated_failure", message)
			emit("node.failed", started, map[string]any{
				"nodeId": node.ID, "tokenId": current.ID,
				"code": "integration.simulated_failure", "errorCode": code, "error": message,
			})
			result.NodeRuns = append(result.NodeRuns, failedNodeRun(node.ID, current, message))
			continue
		}

		nextContext := cloneMap(current.Context)
		nextWrites := cloneWrites(current.Writes)
		if err := applyOperations(node, nextContext, nextWrites); err != nil {
			message := err.Error()
			markFailure("operation.invalid", message)
			emit("node.failed", started, map[string]any{
				"nodeId": node.ID, "tokenId": current.ID, "code": "operation.invalid", "error": message,
			})
			result.NodeRuns = append(result.NodeRuns, failedNodeRun(node.ID, current, message))
			continue
		}
		current.Context, current.Writes = nextContext, nextWrites

		duration := effectiveDuration(node)
		completed := started + duration
		result.NodeTimesMS[node.ID] += duration
		emit("node.completed", completed, map[string]any{
			"nodeId": node.ID, "tokenId": current.ID, "durationMs": duration,
		})
		result.NodeRuns = append(result.NodeRuns, domain.NodeRun{
			NodeID: node.ID, TokenID: current.ID, Status: "success",
			StartedMS: started, CompletedMS: completed, Output: cloneMap(current.Context),
		})

		if node.Type == domain.NodeEnd {
			if configured, ok := node.Configuration["output"]; ok {
				if outputPath, isPath := configured.(string); isPath {
					if value, err := GetValue(current.Context, outputPath); err == nil {
						current.Context = map[string]any{"result": cloneJSON(value)}
					}
				} else {
					current.Context = map[string]any{"result": cloneJSON(configured)}
				}
			}
			outputs = append(outputs, cloneMap(current.Context))
			if configString(node.Configuration, "result", "success") == "failure" {
				markFailure("end.failure", fmt.Sprintf("end node %q reported failure", node.ID))
			}
			continue
		}

		selected, selectionErr := selectEdges(
			node,
			outgoing[node.ID],
			current.Context,
			options.Overrides.ForcedEdges[node.ID],
		)
		if selectionErr != nil {
			message := selectionErr.Error()
			markFailure("condition.invalid", message)
			emit("node.failed", completed, map[string]any{
				"nodeId": node.ID, "tokenId": current.ID, "code": "condition.invalid", "error": message,
			})
			continue
		}

		var forkTemplate *forkFrame
		if len(selected) > 1 {
			nextFork++
			expected := make([]string, len(selected))
			for index, edge := range selected {
				expected[index] = edge.ID
			}
			forkTemplate = &forkFrame{
				ID:        fmt.Sprintf("fork-%06d", nextFork),
				Expected:  expected,
				Iteration: visits[node.ID],
			}
		}
		for _, edge := range selected {
			result.EdgeCounts[edge.ID]++
			emit("edge.traversed", completed, map[string]any{
				"edgeId": edge.ID, "source": edge.Source, "target": edge.Target, "tokenId": current.ID,
			})
			lineage := cloneLineage(current.Lineage)
			if forkTemplate != nil {
				frame := *forkTemplate
				frame.BranchID = edge.ID
				frame.Expected = append([]string(nil), forkTemplate.Expected...)
				lineage = append(lineage, frame)
			}
			enqueue(edge.Target, edge.ID, current.Context, current.Writes, lineage, completed)
		}
	}

	if !limitExceeded && len(joinBarriers) > 0 {
		keys := make([]barrierKey, 0, len(joinBarriers))
		for key := range joinBarriers {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].NodeID != keys[j].NodeID {
				return keys[i].NodeID < keys[j].NodeID
			}
			if keys[i].ForkID != keys[j].ForkID {
				return keys[i].ForkID < keys[j].ForkID
			}
			return keys[i].Iteration < keys[j].Iteration
		})
		for _, key := range keys {
			barrier := joinBarriers[key]
			missing := missingBranches(barrier)
			message := fmt.Sprintf(
				"join %q cannot resolve fork %q; missing branches: %s",
				key.NodeID,
				key.ForkID,
				strings.Join(missing, ","),
			)
			markFailure("run.deadlock", message)
			tokenID := ""
			if arrivals := orderedArrivals(barrier); len(arrivals) > 0 {
				tokenID = arrivals[0].ID
			}
			emit("node.failed", barrier.LogicalMS, map[string]any{
				"nodeId": key.NodeID, "tokenId": tokenID, "forkId": key.ForkID,
				"code": "run.deadlock", "missingBranches": missing, "error": message,
			})
			result.NodeRuns = append(result.NodeRuns, domain.NodeRun{
				NodeID: key.NodeID, TokenID: tokenID, Status: "failed",
				StartedMS: barrier.LogicalMS, CompletedMS: barrier.LogicalMS, Error: message,
			})
		}
	}

	if !limitExceeded && len(outputs) == 0 && !failed {
		markFailure("run.no_end_reached", "no end node was reached")
	}
	result.Output = mergeOutputs(outputs)
	result.Error = runError
	if limitExceeded {
		result.Status = "failed"
		return result, nil
	}

	lastLogical := maxLogicalTime(result.Events)
	if failed {
		result.Status = "failed"
		emit("run.failed", lastLogical, map[string]any{"code": runCode, "error": runError})
	} else {
		result.Status = "completed"
		emit("run.completed", lastLogical, map[string]any{"outputCount": len(outputs)})
	}
	return result, nil
}

func normalizeRunOptions(options *RunOptions) {
	if options.RunID == "" {
		options.RunID = "run"
	}
	if options.MaxSteps <= 0 {
		options.MaxSteps = SimulationDefaultMaxSteps
	}
	if options.MaxVisitsPerNode <= 0 {
		options.MaxVisitsPerNode = SimulationDefaultMaxVisitsPerNode
	}
	if options.StartedAt.IsZero() {
		options.StartedAt = time.Now().UTC()
	}
	if options.Input == nil {
		options.Input = map[string]any{}
	}
	if options.Overrides.ForcedEdges == nil {
		options.Overrides.ForcedEdges = map[string]string{}
	}
	if options.Overrides.FailedNodes == nil {
		options.Overrides.FailedNodes = map[string]string{}
	}
}

func validateRunOptions(options RunOptions) error {
	if options.MaxSteps < 0 || options.MaxSteps > SimulationMaxSteps {
		return fmt.Errorf("maxSteps must be between 1 and %d when provided", SimulationMaxSteps)
	}
	if options.MaxVisitsPerNode < 0 || options.MaxVisitsPerNode > SimulationMaxVisitsPerNode {
		return fmt.Errorf("maxVisitsPerNode must be between 1 and %d when provided", SimulationMaxVisitsPerNode)
	}
	if len(options.Input) > SimulationMaxInputProperties {
		return fmt.Errorf("input must contain at most %d properties", SimulationMaxInputProperties)
	}
	if len(options.Overrides.ForcedEdges)+len(options.Overrides.FailedNodes) > SimulationMaxOverrides {
		return fmt.Errorf("overrides must contain at most %d entries", SimulationMaxOverrides)
	}
	return nil
}

func failedNodeRun(nodeID string, current *token, message string) domain.NodeRun {
	return domain.NodeRun{
		NodeID: nodeID, TokenID: current.ID, Status: "failed",
		StartedMS: current.LogicalMS, CompletedMS: current.LogicalMS, Error: message,
	}
}

func innermostFork(lineage []forkFrame) (forkFrame, bool) {
	if len(lineage) == 0 {
		return forkFrame{}, false
	}
	return lineage[len(lineage)-1], true
}

func popLineage(lineage []forkFrame) []forkFrame {
	if len(lineage) == 0 {
		return nil
	}
	return cloneLineage(lineage[:len(lineage)-1])
}

func cloneLineage(lineage []forkFrame) []forkFrame {
	if len(lineage) == 0 {
		return nil
	}
	result := make([]forkFrame, len(lineage))
	for index, frame := range lineage {
		result[index] = frame
		result[index].Expected = append([]string(nil), frame.Expected...)
	}
	return result
}

func barrierComplete(barrier *joinBarrier) bool {
	if len(barrier.Arrivals) < len(barrier.Expected) {
		return false
	}
	for _, branchID := range barrier.Expected {
		if barrier.Arrivals[branchID] == nil {
			return false
		}
	}
	return true
}

func orderedArrivals(barrier *joinBarrier) []*token {
	arrivals := make([]*token, 0, len(barrier.Expected))
	for _, branchID := range barrier.Expected {
		if arrival := barrier.Arrivals[branchID]; arrival != nil {
			arrivals = append(arrivals, arrival)
		}
	}
	return arrivals
}

func missingBranches(barrier *joinBarrier) []string {
	missing := []string{}
	for _, branchID := range barrier.Expected {
		if barrier.Arrivals[branchID] == nil {
			missing = append(missing, branchID)
		}
	}
	return missing
}

func maxLogicalTime(events []domain.Event) int64 {
	var maximum int64
	for _, event := range events {
		if event.LogicalTimeMS > maximum {
			maximum = event.LogicalTimeMS
		}
	}
	return maximum
}

func effectiveDuration(node domain.Node) int64 {
	duration := node.DurationMS
	switch node.Type {
	case domain.NodeIntegration:
		duration += configInt64(node.Configuration, "latencyMs")
	case domain.NodeDelay:
		duration += configInt64(node.Configuration, "delayMs")
	}
	return duration
}

func withVariableDefaults(flow domain.FlowDefinition, input map[string]any) map[string]any {
	result := cloneMap(input)
	paths := make([]string, 0, len(flow.Variables))
	definitions := make(map[string]domain.VariableDefinition, len(flow.Variables))
	for _, definition := range flow.Variables {
		paths = append(paths, definition.Path)
		definitions[definition.Path] = definition
	}
	sort.Strings(paths)
	for _, path := range paths {
		definition := definitions[path]
		if _, err := GetValue(result, path); err != nil && definition.Default != nil {
			_ = SetValue(result, path, cloneJSON(definition.Default))
		}
	}
	return result
}

func selectEdges(node domain.Node, edges []domain.Edge, context map[string]any, forcedEdge string) ([]domain.Edge, error) {
	if forcedEdge != "" {
		for _, edge := range edges {
			if edge.ID == forcedEdge {
				return []domain.Edge{edge}, nil
			}
		}
		return nil, fmt.Errorf("forced edge %q is not outgoing from node %q", forcedEdge, node.ID)
	}
	if node.Type != domain.NodeDecision {
		selected := []domain.Edge{}
		for _, edge := range edges {
			if edge.Condition == nil {
				selected = append(selected, edge)
				continue
			}
			ok, err := EvaluateCondition(*edge.Condition, context)
			if err != nil {
				return nil, fmt.Errorf("edge %s: %w", edge.ID, err)
			}
			if ok {
				selected = append(selected, edge)
			}
		}
		return selected, nil
	}
	mode := configString(node.Configuration, "strategy", configString(node.Configuration, "mode", "first_match"))
	matched := []domain.Edge{}
	var fallback *domain.Edge
	for index := range edges {
		edge := edges[index]
		if edge.Default {
			copy := edge
			fallback = &copy
			continue
		}
		if edge.Condition == nil {
			continue
		}
		ok, err := EvaluateCondition(*edge.Condition, context)
		if err != nil {
			return nil, fmt.Errorf("edge %s: %w", edge.ID, err)
		}
		if ok {
			matched = append(matched, edge)
			if mode != "all_matches" {
				break
			}
		}
	}
	if len(matched) == 0 && fallback != nil {
		matched = append(matched, *fallback)
	}
	if len(matched) == 0 {
		return nil, fmt.Errorf("decision %q did not select an edge", node.ID)
	}
	return matched, nil
}

type dataOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	From  string `json:"from,omitempty"`
	Value any    `json:"value,omitempty"`
}

func applyOperations(node domain.Node, context map[string]any, writes map[string]writeValue) error {
	rawOperations, ok := node.Configuration["operations"]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(rawOperations)
	if err != nil {
		return err
	}
	var operations []dataOperation
	if err := json.Unmarshal(raw, &operations); err != nil {
		return fmt.Errorf("invalid operations: %w", err)
	}
	for _, operation := range operations {
		switch operation.Op {
		case "set":
			value := cloneJSON(operation.Value)
			if err := SetValue(context, operation.Path, value); err != nil {
				return err
			}
			writes[operation.Path] = writeValue{Value: value}
		case "copy":
			value, err := GetValue(context, operation.From)
			if err != nil {
				return fmt.Errorf("copy from %s: %w", operation.From, err)
			}
			value = cloneJSON(value)
			if err := SetValue(context, operation.Path, value); err != nil {
				return err
			}
			writes[operation.Path] = writeValue{Value: value}
		case "delete":
			if err := DeleteValue(context, operation.Path); err != nil && !errors.Is(err, ErrMissingValue) {
				return err
			}
			writes[operation.Path] = writeValue{Deleted: true}
		default:
			return fmt.Errorf("unsupported operation %q", operation.Op)
		}
	}
	return nil
}

func shouldFail(node domain.Node) bool {
	if node.Type == domain.NodeIntegration && configString(node.Configuration, "outcome", "success") == "failure" {
		return true
	}
	value, _ := node.Configuration["shouldFail"].(bool)
	return value
}

func configString(config map[string]any, key, fallback string) string {
	if value, ok := config[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func configInt64(config map[string]any, key string) int64 {
	value, ok := number(config[key])
	if !ok || value < 0 {
		return 0
	}
	return int64(value)
}

func cloneWrites(value map[string]writeValue) map[string]writeValue {
	result := make(map[string]writeValue, len(value))
	for key, write := range value {
		result[key] = writeValue{Value: cloneJSON(write.Value), Deleted: write.Deleted}
	}
	return result
}

func mergeTokens(input map[string]any, defaults map[string]any, tokens []*token) (map[string]any, map[string]writeValue, error) {
	base := cloneMap(defaults)
	for key, value := range cloneMap(input) {
		base[key] = value
	}
	merged := map[string]writeValue{}
	for _, item := range tokens {
		paths := make([]string, 0, len(item.Writes))
		for path := range item.Writes {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			write := item.Writes[path]
			if previous, exists := merged[path]; exists &&
				(previous.Deleted != write.Deleted || !reflect.DeepEqual(previous.Value, write.Value)) {
				return nil, nil, fmt.Errorf("context.merge_conflict at %s", path)
			}
			merged[path] = writeValue{Value: cloneJSON(write.Value), Deleted: write.Deleted}
		}
	}
	for path, write := range merged {
		if write.Deleted {
			_ = DeleteValue(base, path)
		} else if err := SetValue(base, path, cloneJSON(write.Value)); err != nil {
			return nil, nil, err
		}
	}
	return base, merged, nil
}

func mergeOutputs(outputs []map[string]any) map[string]any {
	if len(outputs) == 0 {
		return map[string]any{}
	}
	if len(outputs) == 1 {
		return outputs[0]
	}
	return map[string]any{"branches": outputs}
}

func cloneJSON(value any) any {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return value
	}
	return result
}

func EventTypes(events []domain.Event) string {
	types := make([]string, len(events))
	for index, event := range events {
		types[index] = event.Type
	}
	return strings.Join(types, ",")
}
