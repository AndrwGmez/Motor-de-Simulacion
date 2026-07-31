package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/flowverse/flowverse-api/internal/domain"
)

func TestEvaluateCondition(t *testing.T) {
	data := map[string]any{
		"payment": map[string]any{"status": "approved", "amount": float64(120)},
		"tags":    []any{"priority", "paid"},
	}
	tests := []struct {
		name      string
		condition domain.Condition
		want      bool
		wantErr   bool
	}{
		{"equals", domain.Condition{Field: "/payment/status", Operator: "equals", Value: "approved"}, true, false},
		{"numeric", domain.Condition{Field: "/payment/amount", Operator: "greater_than", Value: float64(100)}, true, false},
		{"contains", domain.Condition{Field: "/tags", Operator: "contains", Value: "paid"}, true, false},
		{"missing exists", domain.Condition{Field: "/missing", Operator: "exists"}, false, false},
		{"and", domain.Condition{And: []domain.Condition{
			{Field: "/payment/status", Operator: "equals", Value: "approved"},
			{Field: "/payment/amount", Operator: "less_than", Value: float64(200)},
		}}, true, false},
		{"invalid type", domain.Condition{Field: "/payment/status", Operator: "greater_than", Value: float64(1)}, false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := EvaluateCondition(test.condition, data)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestJSONPointerMutations(t *testing.T) {
	data := map[string]any{}
	if err := SetValue(data, "/order/status", "ready"); err != nil {
		t.Fatal(err)
	}
	got, err := GetValue(data, "/order/status")
	if err != nil || got != "ready" {
		t.Fatalf("got %#v, err %v", got, err)
	}
	if err := DeleteValue(data, "/order/status"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetValue(data, "/order/status"); err == nil {
		t.Fatal("deleted value still exists")
	}
}

func TestValidateAndAnalyze(t *testing.T) {
	valid := orderFlow()
	result := Validate(valid)
	if !result.Valid {
		t.Fatalf("valid flow rejected: %+v", result.Issues)
	}
	analysis := Analyze(valid)
	if analysis.NodeCount != 7 || analysis.EdgeCount != 7 || analysis.PathCount != 2 {
		t.Fatalf("unexpected analysis: %+v", analysis)
	}
	if !analysis.CriticalPathApplies || len(analysis.CriticalPathNodeIDs) == 0 {
		t.Fatalf("expected critical path: %+v", analysis)
	}

	invalid := valid.Clone()
	invalid.Edges = append(invalid.Edges, domain.Edge{ID: "broken", Source: "missing", Target: "also-missing"})
	invalid.Nodes = append(invalid.Nodes, domain.Node{ID: "decision-2", Type: domain.NodeDecision, Label: "Broken"})
	validation := Validate(invalid)
	if validation.Valid {
		t.Fatal("invalid flow accepted")
	}
	codes := map[string]bool{}
	for _, issue := range validation.Issues {
		codes[issue.Code] = true
	}
	for _, code := range []string{"edge.source_missing", "edge.target_missing", "decision.no_output", "decision.default_required"} {
		if !codes[code] {
			t.Errorf("missing issue %s", code)
		}
	}
}

func TestAnalyzeCycles(t *testing.T) {
	flow := domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion, Name: "cycle",
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Start"},
			{ID: "loop-a", Type: domain.NodeProcess, Label: "A"},
			{ID: "loop-b", Type: domain.NodeProcess, Label: "B"},
			{ID: "end", Type: domain.NodeEnd, Label: "End"},
		},
		Edges: []domain.Edge{
			{ID: "e1", Source: "start", Target: "loop-a"},
			{ID: "e2", Source: "loop-a", Target: "loop-b"},
			{ID: "e3", Source: "loop-b", Target: "loop-a"},
			{ID: "e4", Source: "loop-b", Target: "end"},
		},
	}
	analysis := Analyze(flow)
	if len(analysis.Cycles) != 1 || !analysis.Cycles[0].HasExit || analysis.CriticalPathApplies {
		t.Fatalf("unexpected cycles: %+v", analysis)
	}
	if !Validate(flow).Valid {
		t.Fatal("cycle with a structural exit should be executable with runtime bounds")
	}
}

func TestSimulatorGoldenOrderFlow(t *testing.T) {
	flow := orderFlow()
	result, err := NewSimulator().Run(flow, RunOptions{
		RunID: "run-order", TriggerID: "start",
		Input:     map[string]any{"payment": map[string]any{"status": "approved"}},
		StartedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Fatalf("status %s: %s", result.Status, result.Error)
	}
	got := EventTypes(result.Events)
	goldenPath := filepath.Join("testdata", "order_events.golden.txt")
	wantRaw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(wantRaw))
	if got != want {
		t.Fatalf("event stream mismatch\nwant: %s\n got: %s", want, got)
	}
	wantPath := []string{"start", "validate-payment", "approved", "prepare", "send", "done"}
	if !reflect.DeepEqual(result.VisitedPath, wantPath) {
		t.Fatalf("path %#v, want %#v", result.VisitedPath, wantPath)
	}
}

func TestSimulatorDefaultAndOverride(t *testing.T) {
	flow := orderFlow()
	simulator := NewSimulator()
	rejected, err := simulator.Run(flow, RunOptions{
		RunID: "rejected", TriggerID: "start",
		Input: map[string]any{"payment": map[string]any{"status": "rejected"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(rejected.VisitedPath, "refund") || containsString(rejected.VisitedPath, "prepare") {
		t.Fatalf("unexpected default path: %#v", rejected.VisitedPath)
	}
	forced, err := simulator.Run(flow, RunOptions{
		RunID: "forced", TriggerID: "start",
		Input:     map[string]any{"payment": map[string]any{"status": "rejected"}},
		Overrides: RunOverrides{ForcedEdges: map[string]string{"approved": "approved-yes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(forced.VisitedPath, "prepare") {
		t.Fatalf("override not used: %#v", forced.VisitedPath)
	}
}

func TestSimulatorFanOutJoinAllAndConflict(t *testing.T) {
	flow := forkJoinFlow(false)
	result, err := NewSimulator().Run(flow, RunOptions{RunID: "join", TriggerID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || countString(result.VisitedPath, "join") != 1 {
		t.Fatalf("join result: %+v", result)
	}
	if countEventType(result.Events, "node.waiting") != 1 {
		t.Fatalf("join must emit one waiting arrival: %s", EventTypes(result.Events))
	}
	if value, err := GetValue(result.Output, "/left"); err != nil || value != "ok" {
		t.Fatalf("left output = %#v, err %v", value, err)
	}
	if value, err := GetValue(result.Output, "/right"); err != nil || value != "ok" {
		t.Fatalf("right output = %#v, err %v", value, err)
	}

	conflict := forkJoinFlow(true)
	failed, err := NewSimulator().Run(conflict, RunOptions{RunID: "conflict", TriggerID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" || !eventHasCode(failed.Events, "context.merge_conflict") {
		t.Fatalf("expected merge conflict: %+v", failed)
	}
}

func TestSimulatorLimitsAndForcedFailure(t *testing.T) {
	flow := orderFlow()
	result, err := NewSimulator().Run(flow, RunOptions{
		RunID: "failure", TriggerID: "start",
		Input:     map[string]any{"payment": map[string]any{"status": "approved"}},
		Overrides: RunOverrides{FailedNodes: map[string]string{"prepare": "warehouse unavailable"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || !strings.Contains(mustJSON(result.Events), "warehouse unavailable") {
		t.Fatalf("expected forced failure: %+v", result)
	}
}

func TestSimulatorJoinAllFailsAnUnresolvableBarrier(t *testing.T) {
	flow := domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion,
		Name:          "Unresolvable join",
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Start"},
			{ID: "fork", Type: domain.NodeProcess, Label: "Fork"},
			{ID: "left", Type: domain.NodeProcess, Label: "Left"},
			{ID: "right-end", Type: domain.NodeEnd, Label: "Right end"},
			{ID: "join", Type: domain.NodeProcess, Label: "Join", ActivationMode: domain.ActivationAll},
			{ID: "joined-end", Type: domain.NodeEnd, Label: "Joined end"},
		},
		Edges: []domain.Edge{
			{ID: "start-fork", Source: "start", Target: "fork"},
			{ID: "fork-left", Source: "fork", Target: "left"},
			{ID: "fork-right", Source: "fork", Target: "right-end"},
			{ID: "left-join", Source: "left", Target: "join"},
			{ID: "join-end", Source: "join", Target: "joined-end"},
		},
	}

	result, err := NewSimulator().Run(flow, RunOptions{RunID: "deadlock", TriggerID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || !eventHasCode(result.Events, "run.deadlock") {
		t.Fatalf("unresolved join must fail explicitly: %+v", result)
	}
	if countEventType(result.Events, "node.waiting") != 1 {
		t.Fatalf("expected a waiting event: %s", EventTypes(result.Events))
	}
	if countEventType(result.Events, "run.completed") != 0 {
		t.Fatalf("deadlocked run completed: %s", EventTypes(result.Events))
	}
}

func TestSimulatorAnyIsCorrelatedPerFork(t *testing.T) {
	flow := domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion,
		Name:          "Independent any joins",
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Start"},
			{ID: "outer", Type: domain.NodeProcess, Label: "Outer fork"},
			{ID: "fork-a", Type: domain.NodeProcess, Label: "Fork A"},
			{ID: "fork-b", Type: domain.NodeProcess, Label: "Fork B"},
			{ID: "a-one", Type: domain.NodeProcess, Label: "A1"},
			{ID: "a-two", Type: domain.NodeProcess, Label: "A2"},
			{ID: "b-one", Type: domain.NodeProcess, Label: "B1"},
			{ID: "b-two", Type: domain.NodeProcess, Label: "B2"},
			{ID: "any-join", Type: domain.NodeProcess, Label: "Any", ActivationMode: domain.ActivationAny},
			{ID: "end", Type: domain.NodeEnd, Label: "End"},
		},
		Edges: []domain.Edge{
			{ID: "start-outer", Source: "start", Target: "outer"},
			{ID: "outer-a", Source: "outer", Target: "fork-a"},
			{ID: "outer-b", Source: "outer", Target: "fork-b"},
			{ID: "a-1", Source: "fork-a", Target: "a-one"},
			{ID: "a-2", Source: "fork-a", Target: "a-two"},
			{ID: "b-1", Source: "fork-b", Target: "b-one"},
			{ID: "b-2", Source: "fork-b", Target: "b-two"},
			{ID: "a1-any", Source: "a-one", Target: "any-join"},
			{ID: "a2-any", Source: "a-two", Target: "any-join"},
			{ID: "b1-any", Source: "b-one", Target: "any-join"},
			{ID: "b2-any", Source: "b-two", Target: "any-join"},
			{ID: "any-end", Source: "any-join", Target: "end"},
		},
	}

	result, err := NewSimulator().Run(flow, RunOptions{RunID: "any", TriggerID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Fatalf("status %s: %s", result.Status, result.Error)
	}
	if got := countString(result.VisitedPath, "any-join"); got != 2 {
		t.Fatalf("any join visits = %d, want one per independent inner fork", got)
	}
	if got := countEventType(result.Events, "node.skipped"); got != 2 {
		t.Fatalf("skipped siblings = %d, want 2: %s", got, EventTypes(result.Events))
	}
}

func TestSimulatorAddsBaseAndConfiguredDurations(t *testing.T) {
	flow := domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion,
		Name:          "Durations",
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Start"},
			{
				ID: "integration", Type: domain.NodeIntegration, Label: "Integration", DurationMS: 50,
				Configuration: map[string]any{"latencyMs": 800, "outcome": "success"},
			},
			{
				ID: "delay", Type: domain.NodeDelay, Label: "Delay", DurationMS: 10,
				Configuration: map[string]any{"delayMs": 100},
			},
			{ID: "end", Type: domain.NodeEnd, Label: "End"},
		},
		Edges: []domain.Edge{
			{ID: "e1", Source: "start", Target: "integration"},
			{ID: "e2", Source: "integration", Target: "delay"},
			{ID: "e3", Source: "delay", Target: "end"},
		},
	}

	result, err := NewSimulator().Run(flow, RunOptions{RunID: "duration", TriggerID: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if result.NodeTimesMS["integration"] != 850 {
		t.Fatalf("integration duration = %d, want 850", result.NodeTimesMS["integration"])
	}
	if result.NodeTimesMS["delay"] != 110 {
		t.Fatalf("delay duration = %d, want 110", result.NodeTimesMS["delay"])
	}
	if event := lastNodeEvent(result.Events, "end", "node.completed"); event == nil || event.LogicalTimeMS != 960 {
		t.Fatalf("end logical time = %#v, want 960", event)
	}
}

func TestSimulatorEmitsLimitEventsAndTerminatesCycles(t *testing.T) {
	t.Run("max visits", func(t *testing.T) {
		flow := cycleFlow(false)
		result, err := NewSimulator().Run(flow, RunOptions{
			RunID: "visit-limit", TriggerID: "start",
			Input: map[string]any{"keepLooping": true}, MaxVisitsPerNode: 3,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "failed" || result.Events[len(result.Events)-1].Type != "run.limit_exceeded" {
			t.Fatalf("expected terminal limit event: %+v", result)
		}
		if !eventHasCode(result.Events, "run.max_visits_per_node") {
			t.Fatalf("missing visit limit code: %s", mustJSON(result.Events))
		}
	})

	t.Run("max steps", func(t *testing.T) {
		result, err := NewSimulator().Run(orderFlow(), RunOptions{
			RunID: "step-limit", TriggerID: "start",
			Input:    map[string]any{"payment": map[string]any{"status": "approved"}},
			MaxSteps: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "failed" || !eventHasCode(result.Events, "run.max_steps") {
			t.Fatalf("missing step limit event: %+v", result)
		}
	})

	t.Run("cycle exits after mutation", func(t *testing.T) {
		result, err := NewSimulator().Run(cycleFlow(true), RunOptions{
			RunID: "terminating-cycle", TriggerID: "start",
			Input: map[string]any{"keepLooping": true},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "completed" || countString(result.VisitedPath, "decision") != 2 {
			t.Fatalf("terminating cycle result: %+v", result)
		}
	})
}

func cycleFlow(mutates bool) domain.FlowDefinition {
	worker := domain.Node{ID: "worker", Type: domain.NodeProcess, Label: "Worker"}
	if mutates {
		worker.Type = domain.NodeData
		worker.Configuration = map[string]any{
			"operations": []any{map[string]any{"op": "set", "path": "/keepLooping", "value": false}},
		}
	}
	return domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion,
		Name:          "Bounded cycle",
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Start"},
			{
				ID: "decision", Type: domain.NodeDecision, Label: "Again?",
				Configuration: map[string]any{"strategy": "first_match"},
			},
			worker,
			{ID: "end", Type: domain.NodeEnd, Label: "End"},
		},
		Edges: []domain.Edge{
			{ID: "start-decision", Source: "start", Target: "decision"},
			{
				ID: "decision-loop", Source: "decision", Target: "worker", Priority: 1,
				Condition: &domain.Condition{Field: "/keepLooping", Operator: "equals", Value: true},
			},
			{ID: "decision-end", Source: "decision", Target: "end", Priority: 2, Default: true},
			{ID: "worker-decision", Source: "worker", Target: "decision"},
		},
	}
}

func orderFlow() domain.FlowDefinition {
	return domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion, Name: "Orders",
		Variables: []domain.VariableDefinition{
			{Path: "/payment/status", Type: "string", Required: true},
		},
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Order received"},
			{ID: "validate-payment", Type: domain.NodeProcess, Label: "Validate payment", DurationMS: 10},
			{ID: "approved", Type: domain.NodeDecision, Label: "Approved?", Configuration: map[string]any{"mode": "first_match"}},
			{ID: "prepare", Type: domain.NodeProcess, Label: "Prepare", DurationMS: 20},
			{ID: "refund", Type: domain.NodeProcess, Label: "Refund", DurationMS: 15},
			{ID: "send", Type: domain.NodeProcess, Label: "Send", DurationMS: 30},
			{ID: "done", Type: domain.NodeEnd, Label: "Done"},
		},
		Edges: []domain.Edge{
			{ID: "start-payment", Source: "start", Target: "validate-payment"},
			{ID: "payment-decision", Source: "validate-payment", Target: "approved"},
			{ID: "approved-yes", Source: "approved", Target: "prepare", Priority: 1, Condition: &domain.Condition{Field: "/payment/status", Operator: "equals", Value: "approved"}},
			{ID: "approved-no", Source: "approved", Target: "refund", Priority: 2, Default: true},
			{ID: "prepare-send", Source: "prepare", Target: "send"},
			{ID: "send-done", Source: "send", Target: "done"},
			{ID: "refund-done", Source: "refund", Target: "done"},
		},
	}
}

func forkJoinFlow(conflict bool) domain.FlowDefinition {
	leftPath, rightPath := "/left", "/right"
	if conflict {
		leftPath, rightPath = "/shared", "/shared"
	}
	return domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion, Name: "Fork join",
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger, Label: "Start"},
			{ID: "fork", Type: domain.NodeProcess, Label: "Fork"},
			{ID: "left", Type: domain.NodeData, Label: "Left", Configuration: map[string]any{"operations": []any{map[string]any{"op": "set", "path": leftPath, "value": "ok"}}}},
			{ID: "right", Type: domain.NodeData, Label: "Right", Configuration: map[string]any{"operations": []any{map[string]any{"op": "set", "path": rightPath, "value": map[bool]string{true: "different", false: "ok"}[conflict]}}}},
			{ID: "join", Type: domain.NodeProcess, Label: "Join", ActivationMode: domain.ActivationAll},
			{ID: "end", Type: domain.NodeEnd, Label: "End"},
		},
		Edges: []domain.Edge{
			{ID: "e1", Source: "start", Target: "fork"},
			{ID: "e2", Source: "fork", Target: "left"},
			{ID: "e3", Source: "fork", Target: "right"},
			{ID: "e4", Source: "left", Target: "join"},
			{ID: "e5", Source: "right", Target: "join"},
			{ID: "e6", Source: "join", Target: "end"},
		},
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func countString(values []string, wanted string) int {
	count := 0
	for _, value := range values {
		if value == wanted {
			count++
		}
	}
	return count
}

func countEventType(events []domain.Event, wanted string) int {
	count := 0
	for _, event := range events {
		if event.Type == wanted {
			count++
		}
	}
	return count
}

func lastNodeEvent(events []domain.Event, nodeID, eventType string) *domain.Event {
	for index := len(events) - 1; index >= 0; index-- {
		event := &events[index]
		if event.Type == eventType && event.Payload["nodeId"] == nodeID {
			return event
		}
	}
	return nil
}

func eventHasCode(events []domain.Event, wanted string) bool {
	for _, event := range events {
		if event.Payload["code"] == wanted {
			return true
		}
	}
	return false
}

func mustJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
