package engine

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/flowverse/flowverse-api/internal/domain"
)

func TestCriticalPathIgnoresSlowerDisconnectedChainAndUsesEffectiveDuration(t *testing.T) {
	flow := domain.FlowDefinition{
		SchemaVersion: domain.SchemaVersion,
		Name:          "Relevant critical path",
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger},
			{ID: "service", Type: domain.NodeIntegration, DurationMS: 5, Configuration: map[string]any{"latencyMs": 20, "outcome": "success"}},
			{ID: "delay", Type: domain.NodeDelay, DurationMS: 2, Configuration: map[string]any{"delayMs": 8}},
			{ID: "end", Type: domain.NodeEnd},
			{ID: "slow-a", Type: domain.NodeProcess, DurationMS: 10_000},
			{ID: "slow-b", Type: domain.NodeEnd, DurationMS: 10_000},
			{ID: "visual", Type: domain.NodeGroup, DurationMS: 99_999},
		},
		Edges: []domain.Edge{
			{ID: "e1", Source: "start", Target: "service"},
			{ID: "e2", Source: "service", Target: "delay"},
			{ID: "e3", Source: "delay", Target: "end"},
			{ID: "disconnected", Source: "slow-a", Target: "slow-b"},
			{ID: "group-edge", Source: "visual", Target: "slow-b"},
			{ID: "invalid", Source: "missing", Target: "slow-b"},
		},
	}

	analysis := Analyze(flow)
	wantPath := []string{"start", "service", "delay", "end"}
	if !analysis.CriticalPathApplies || !reflect.DeepEqual(analysis.CriticalPathNodeIDs, wantPath) {
		t.Fatalf("critical path = %#v applies=%v, want %#v", analysis.CriticalPathNodeIDs, analysis.CriticalPathApplies, wantPath)
	}
	if analysis.CriticalPathMS != 35 {
		t.Fatalf("critical path duration = %d, want 35", analysis.CriticalPathMS)
	}
}

func TestCountPathsMarksOnlyActualTruncationAtLimit(t *testing.T) {
	for _, test := range []struct {
		name          string
		paths         int
		wantCount     int
		wantTruncated bool
	}{
		{name: "exactly at limit", paths: 100, wantCount: 100, wantTruncated: false},
		{name: "above limit", paths: 101, wantCount: 100, wantTruncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			flow, adj := parallelEndPaths(test.paths)
			count, truncated := countPaths(flow, adj, 100)
			if count != test.wantCount || truncated != test.wantTruncated {
				t.Fatalf("count=%d truncated=%v, want count=%d truncated=%v",
					count, truncated, test.wantCount, test.wantTruncated)
			}
		})
	}
}

func TestCountPathsSaturatesAdversarialDAGWithoutEnumeration(t *testing.T) {
	flow := domain.FlowDefinition{
		Nodes: []domain.Node{{ID: "start", Type: domain.NodeTrigger}},
	}
	adj := map[string][]string{}
	previous := []string{"start"}
	for layer := 0; layer < 40; layer++ {
		current := []string{
			fmt.Sprintf("layer-%02d-a", layer),
			fmt.Sprintf("layer-%02d-b", layer),
		}
		for _, id := range current {
			flow.Nodes = append(flow.Nodes, domain.Node{ID: id, Type: domain.NodeProcess})
		}
		for _, source := range previous {
			adj[source] = append(adj[source], current...)
		}
		previous = current
	}
	flow.Nodes = append(flow.Nodes, domain.Node{ID: "end", Type: domain.NodeEnd})
	for _, source := range previous {
		adj[source] = append(adj[source], "end")
	}

	count, truncated := countPaths(flow, adj, 100)
	if count != 100 || !truncated {
		t.Fatalf("count=%d truncated=%v, want saturated count", count, truncated)
	}
}

func TestCountPathsCyclicFallbackHasHardWorkBudget(t *testing.T) {
	flow := domain.FlowDefinition{
		Nodes: []domain.Node{
			{ID: "start", Type: domain.NodeTrigger},
			{ID: "end", Type: domain.NodeEnd},
		},
	}
	adj := map[string][]string{"start": {"node-00"}}
	for index := 0; index < 10; index++ {
		id := fmt.Sprintf("node-%02d", index)
		flow.Nodes = append(flow.Nodes, domain.Node{ID: id, Type: domain.NodeProcess})
		for target := 0; target < 10; target++ {
			if target != index {
				adj[id] = append(adj[id], fmt.Sprintf("node-%02d", target))
			}
		}
		adj[id] = append(adj[id], "end")
	}

	count, truncated := countPathsWithBudget(flow, adj, 100, 64)
	if !truncated {
		t.Fatalf("cyclic combinatorial graph exhausted without reporting budget truncation; count=%d", count)
	}
	if count > 100 {
		t.Fatalf("count=%d exceeded the public cap", count)
	}
}

func TestCountPathsPrunesCycleWithoutTerminalPath(t *testing.T) {
	flow := domain.FlowDefinition{Nodes: []domain.Node{
		{ID: "start", Type: domain.NodeTrigger},
		{ID: "a", Type: domain.NodeProcess},
		{ID: "b", Type: domain.NodeProcess},
		{ID: "unreachable-end", Type: domain.NodeEnd},
	}}
	adj := map[string][]string{"start": {"a"}, "a": {"b"}, "b": {"a"}}

	count, truncated := countPathsWithBudget(flow, adj, 100, 1)
	if count != 0 || truncated {
		t.Fatalf("count=%d truncated=%v, want exact zero after reverse pruning", count, truncated)
	}
}

func parallelEndPaths(paths int) (domain.FlowDefinition, map[string][]string) {
	flow := domain.FlowDefinition{Nodes: []domain.Node{{ID: "start", Type: domain.NodeTrigger}}}
	adj := map[string][]string{}
	for index := 0; index < paths; index++ {
		id := fmt.Sprintf("end-%03d", index)
		flow.Nodes = append(flow.Nodes, domain.Node{ID: id, Type: domain.NodeEnd})
		adj["start"] = append(adj["start"], id)
	}
	return flow, adj
}
