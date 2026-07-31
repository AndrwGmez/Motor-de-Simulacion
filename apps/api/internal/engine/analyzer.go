package engine

import (
	"sort"

	"github.com/flowverse/flowverse-api/internal/domain"
)

func Analyze(flow domain.FlowDefinition) domain.Analysis {
	result := domain.Analysis{
		NodeCount: len(flow.Nodes), EdgeCount: len(flow.Edges),
		CriticalPathApplies: true,
	}
	nodes := map[string]domain.Node{}
	adj, reverse := map[string][]string{}, map[string][]string{}
	for _, node := range flow.Nodes {
		nodes[node.ID] = node
		if node.Type == domain.NodeTrigger {
			result.TriggerCount++
		}
		if node.Type == domain.NodeEnd {
			result.EndCount++
		}
	}
	for _, edge := range flow.Edges {
		if _, ok := nodes[edge.Source]; !ok {
			continue
		}
		if _, ok := nodes[edge.Target]; !ok {
			continue
		}
		adj[edge.Source] = append(adj[edge.Source], edge.Target)
		reverse[edge.Target] = append(reverse[edge.Target], edge.Source)
	}
	for id := range adj {
		sort.Strings(adj[id])
	}
	reachable := map[string]bool{}
	queue := []string{}
	for id, node := range nodes {
		if node.Type == domain.NodeTrigger {
			reachable[id] = true
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, target := range adj[id] {
			if !reachable[target] {
				reachable[target] = true
				queue = append(queue, target)
			}
		}
	}
	for id, node := range nodes {
		if node.Type != domain.NodeGroup && !reachable[id] {
			result.UnreachableNodeIDs = append(result.UnreachableNodeIDs, id)
		}
		if node.Type != domain.NodeGroup && len(adj[id]) == 0 && len(reverse[id]) == 0 {
			result.DisconnectedNodeIDs = append(result.DisconnectedNodeIDs, id)
		}
		if len(adj[id])+len(reverse[id]) >= 5 {
			result.BottleneckNodeIDs = append(result.BottleneckNodeIDs, id)
		}
	}
	sort.Strings(result.UnreachableNodeIDs)
	sort.Strings(result.DisconnectedNodeIDs)
	sort.Strings(result.BottleneckNodeIDs)

	components := stronglyConnected(nodes, adj)
	componentOf := map[string]int{}
	for index, component := range components {
		for _, id := range component {
			componentOf[id] = index
		}
		isCycle := len(component) > 1
		if len(component) == 1 {
			for _, target := range adj[component[0]] {
				if target == component[0] {
					isCycle = true
					break
				}
			}
		}
		if isCycle {
			member := map[string]bool{}
			for _, id := range component {
				member[id] = true
			}
			hasExit := false
			for _, id := range component {
				for _, target := range adj[id] {
					if !member[target] {
						hasExit = true
					}
				}
			}
			sort.Strings(component)
			result.Cycles = append(result.Cycles, domain.Cycle{NodeIDs: component, HasExit: hasExit})
		}
	}
	if len(result.Cycles) > 0 {
		result.CriticalPathApplies = false
	}

	// Compute depth on the SCC-condensed DAG.
	dag := map[int]map[int]bool{}
	indegree := make([]int, len(components))
	for source, targets := range adj {
		for _, target := range targets {
			a, b := componentOf[source], componentOf[target]
			if a != b {
				if dag[a] == nil {
					dag[a] = map[int]bool{}
				}
				if !dag[a][b] {
					dag[a][b] = true
					indegree[b]++
				}
			}
		}
	}
	componentQueue := []int{}
	depth := make([]int, len(components))
	for i, degree := range indegree {
		if degree == 0 {
			componentQueue = append(componentQueue, i)
		}
	}
	for len(componentQueue) > 0 {
		current := componentQueue[0]
		componentQueue = componentQueue[1:]
		for next := range dag[current] {
			if depth[next] < depth[current]+1 {
				depth[next] = depth[current] + 1
			}
			if depth[next] > result.MaxDepth {
				result.MaxDepth = depth[next]
			}
			indegree[next]--
			if indegree[next] == 0 {
				componentQueue = append(componentQueue, next)
			}
		}
	}
	if len(flow.Nodes) > 0 {
		connectedComponents := undirectedComponentCount(nodes, adj, reverse)
		result.CyclomaticComplexity = len(flow.Edges) - len(flow.Nodes) + 2*connectedComponents
		if result.CyclomaticComplexity < 1 {
			result.CyclomaticComplexity = 1
		}
	}

	result.PathCount, result.PathsTruncated = countPaths(flow, adj, 100)
	if result.CriticalPathApplies {
		result.CriticalPathNodeIDs, result.CriticalPathMS = longestPath(flow, adj, reverse)
	}
	return result
}

func stronglyConnected(nodes map[string]domain.Node, adj map[string][]string) [][]string {
	index := 0
	indices, low := map[string]int{}, map[string]int{}
	onStack := map[string]bool{}
	stack := []string{}
	components := [][]string{}
	var visit func(string)
	visit = func(id string) {
		indices[id], low[id] = index, index
		index++
		stack = append(stack, id)
		onStack[id] = true
		for _, target := range adj[id] {
			if _, seen := indices[target]; !seen {
				visit(target)
				if low[target] < low[id] {
					low[id] = low[target]
				}
			} else if onStack[target] && indices[target] < low[id] {
				low[id] = indices[target]
			}
		}
		if low[id] == indices[id] {
			component := []string{}
			for {
				last := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[last] = false
				component = append(component, last)
				if last == id {
					break
				}
			}
			components = append(components, component)
		}
	}
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, seen := indices[id]; !seen {
			visit(id)
		}
	}
	return components
}

func undirectedComponentCount(nodes map[string]domain.Node, adj, reverse map[string][]string) int {
	seen, count := map[string]bool{}, 0
	for id := range nodes {
		if seen[id] {
			continue
		}
		count++
		queue := []string{id}
		seen[id] = true
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, next := range append(adj[current], reverse[current]...) {
				if !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
	}
	return count
}

func countPaths(flow domain.FlowDefinition, adj map[string][]string, limit int) (int, bool) {
	nodes := map[string]domain.Node{}
	triggers := []string{}
	for _, node := range flow.Nodes {
		nodes[node.ID] = node
		if node.Type == domain.NodeTrigger {
			triggers = append(triggers, node.ID)
		}
	}
	count, truncated := 0, false
	var walk func(string, map[string]bool)
	walk = func(id string, path map[string]bool) {
		if truncated {
			return
		}
		if path[id] {
			return
		}
		if nodes[id].Type == domain.NodeEnd {
			count++
			if count >= limit {
				truncated = true
			}
			return
		}
		nextPath := make(map[string]bool, len(path)+1)
		for key, value := range path {
			nextPath[key] = value
		}
		nextPath[id] = true
		for _, target := range adj[id] {
			walk(target, nextPath)
		}
	}
	sort.Strings(triggers)
	for _, trigger := range triggers {
		walk(trigger, map[string]bool{})
	}
	return count, truncated
}

func longestPath(flow domain.FlowDefinition, adj, reverse map[string][]string) ([]string, int64) {
	nodes := map[string]domain.Node{}
	indegree := map[string]int{}
	for _, node := range flow.Nodes {
		nodes[node.ID] = node
	}
	for id := range nodes {
		indegree[id] = len(reverse[id])
	}
	queue := []string{}
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	distance := map[string]int64{}
	previous := map[string]string{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		base := distance[id] + nodes[id].DurationMS
		for _, target := range adj[id] {
			currentDistance, reached := distance[target]
			if !reached || base > currentDistance {
				distance[target] = base
				previous[target] = id
			}
			indegree[target]--
			if indegree[target] == 0 {
				queue = append(queue, target)
				sort.Strings(queue)
			}
		}
	}
	bestID := ""
	var best int64
	for id, node := range nodes {
		value := distance[id] + node.DurationMS
		if value > best || value == best && id < bestID {
			best, bestID = value, id
		}
	}
	path := []string{}
	for id := bestID; id != ""; id = previous[id] {
		path = append(path, id)
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	return path, best
}
