package engine

import (
	"sort"

	"github.com/flowverse/flowverse-api/internal/domain"
)

// Cyclic graphs require enumerating simple paths, which is exponential in the
// worst case. DAGs are counted exactly with dynamic programming; this budget is
// only the hard stop for the cyclic fallback.
const pathCountWorkBudget = 100_000

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
	result.CriticalPathNodeIDs, result.CriticalPathMS, result.CriticalPathApplies = longestPath(flow)
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
	return countPathsWithBudget(flow, adj, limit, pathCountWorkBudget)
}

func countPathsWithBudget(flow domain.FlowDefinition, adj map[string][]string, limit, workBudget int) (int, bool) {
	if limit <= 0 {
		return 0, false
	}
	nodes := map[string]domain.Node{}
	triggers := []string{}
	for _, node := range flow.Nodes {
		nodes[node.ID] = node
		if node.Type == domain.NodeTrigger {
			triggers = append(triggers, node.ID)
		}
	}
	sort.Strings(triggers)

	// Ignore malformed references and stop traversal at end nodes, matching the
	// simulator and the previous path-count semantics.
	graph := make(map[string][]string, len(nodes))
	reverse := make(map[string][]string, len(nodes))
	for source, targets := range adj {
		node, exists := nodes[source]
		if !exists || node.Type == domain.NodeEnd {
			continue
		}
		for _, target := range targets {
			if _, exists := nodes[target]; !exists {
				continue
			}
			graph[source] = append(graph[source], target)
			reverse[target] = append(reverse[target], source)
		}
	}
	for id := range graph {
		sort.Strings(graph[id])
	}

	reachable := map[string]bool{}
	queue := make([]string, 0, len(nodes))
	for _, trigger := range triggers {
		if !reachable[trigger] {
			reachable[trigger] = true
			queue = append(queue, trigger)
		}
	}
	for head := 0; head < len(queue); head++ {
		for _, target := range graph[queue[head]] {
			if !reachable[target] {
				reachable[target] = true
				queue = append(queue, target)
			}
		}
	}

	// Pruning nodes that cannot reach an end avoids spending the cyclic budget
	// on dead subgraphs and lets us return an exact zero when no path can finish.
	canReachEnd := map[string]bool{}
	queue = queue[:0]
	for id, node := range nodes {
		if reachable[id] && node.Type == domain.NodeEnd {
			canReachEnd[id] = true
			queue = append(queue, id)
		}
	}
	for head := 0; head < len(queue); head++ {
		for _, source := range reverse[queue[head]] {
			if reachable[source] && !canReachEnd[source] {
				canReachEnd[source] = true
				queue = append(queue, source)
			}
		}
	}
	relevantCount := 0
	for id := range nodes {
		if reachable[id] && canReachEnd[id] {
			relevantCount++
		}
	}
	if relevantCount == 0 {
		return 0, false
	}

	// A topological pass detects the common acyclic case and enables exact,
	// saturated dynamic programming in O(V+E), even when the number of paths is
	// astronomically large.
	indegree := map[string]int{}
	for id := range nodes {
		if reachable[id] && canReachEnd[id] {
			indegree[id] = 0
		}
	}
	for source, targets := range graph {
		if _, relevant := indegree[source]; !relevant {
			continue
		}
		for _, target := range targets {
			if _, relevant := indegree[target]; relevant {
				indegree[target]++
			}
		}
	}
	topologicalQueue := make([]string, 0, relevantCount)
	for id, degree := range indegree {
		if degree == 0 {
			topologicalQueue = append(topologicalQueue, id)
		}
	}
	sort.Strings(topologicalQueue)
	order := make([]string, 0, relevantCount)
	for head := 0; head < len(topologicalQueue); head++ {
		id := topologicalQueue[head]
		order = append(order, id)
		for _, target := range graph[id] {
			if _, relevant := indegree[target]; !relevant {
				continue
			}
			indegree[target]--
			if indegree[target] == 0 {
				topologicalQueue = append(topologicalQueue, target)
			}
		}
	}
	if len(order) == relevantCount {
		cap := limit + 1
		pathsFrom := make(map[string]int, relevantCount)
		for index := len(order) - 1; index >= 0; index-- {
			id := order[index]
			if nodes[id].Type == domain.NodeEnd {
				pathsFrom[id] = 1
				continue
			}
			for _, target := range graph[id] {
				if !canReachEnd[target] {
					continue
				}
				pathsFrom[id] = saturatedAdd(pathsFrom[id], pathsFrom[target], cap)
			}
		}
		count := 0
		for _, trigger := range triggers {
			count = saturatedAdd(count, pathsFrom[trigger], cap)
		}
		if count > limit {
			return limit, true
		}
		return count, false
	}

	// Exact simple-path counting is #P-complete once cycles are present. Use an
	// iterative DFS (bounded stack, no per-branch map copies) and report a
	// conservative truncation if its deterministic work budget is exhausted.
	type pathFrame struct {
		id      string
		next    int
		entered bool
	}
	count, work := 0, 0
	path := map[string]bool{}
	for _, trigger := range triggers {
		if !canReachEnd[trigger] {
			continue
		}
		stack := []pathFrame{{id: trigger}}
		for len(stack) > 0 {
			frame := &stack[len(stack)-1]
			if !frame.entered {
				if work >= workBudget {
					return count, true
				}
				work++
				frame.entered = true
				if nodes[frame.id].Type == domain.NodeEnd {
					count++
					stack = stack[:len(stack)-1]
					if count > limit {
						return limit, true
					}
					continue
				}
				path[frame.id] = true
			}
			if frame.next >= len(graph[frame.id]) {
				delete(path, frame.id)
				stack = stack[:len(stack)-1]
				continue
			}
			if work >= workBudget {
				return count, true
			}
			work++
			target := graph[frame.id][frame.next]
			frame.next++
			if path[target] || !canReachEnd[target] {
				continue
			}
			stack = append(stack, pathFrame{id: target})
		}
	}
	return count, false
}

func saturatedAdd(left, right, cap int) int {
	if left >= cap || right >= cap-left {
		return cap
	}
	return left + right
}

// longestPath computes an executable trigger-to-end path only. Visual groups,
// malformed references, disconnected components and dead branches cannot be a
// production critical path. Cycles matter only when they belong to that
// reachable-and-terminating subgraph.
func longestPath(flow domain.FlowDefinition) ([]string, int64, bool) {
	nodes := map[string]domain.Node{}
	triggers := []string{}
	for _, node := range flow.Nodes {
		if node.Type == domain.NodeGroup || node.ID == "" {
			continue
		}
		nodes[node.ID] = node
		if node.Type == domain.NodeTrigger {
			triggers = append(triggers, node.ID)
		}
	}
	sort.Strings(triggers)

	adj, reverse := map[string][]string{}, map[string][]string{}
	for _, edge := range flow.Edges {
		source, sourceOK := nodes[edge.Source]
		if _, targetOK := nodes[edge.Target]; !sourceOK || !targetOK || source.Type == domain.NodeEnd {
			continue
		}
		adj[edge.Source] = append(adj[edge.Source], edge.Target)
		reverse[edge.Target] = append(reverse[edge.Target], edge.Source)
	}
	for id := range adj {
		sort.Strings(adj[id])
	}
	for id := range reverse {
		sort.Strings(reverse[id])
	}

	reachable := map[string]bool{}
	queue := append([]string(nil), triggers...)
	for _, trigger := range triggers {
		reachable[trigger] = true
	}
	for head := 0; head < len(queue); head++ {
		for _, target := range adj[queue[head]] {
			if !reachable[target] {
				reachable[target] = true
				queue = append(queue, target)
			}
		}
	}

	canReachEnd := map[string]bool{}
	queue = queue[:0]
	for id, node := range nodes {
		if reachable[id] && node.Type == domain.NodeEnd {
			canReachEnd[id] = true
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	for head := 0; head < len(queue); head++ {
		for _, source := range reverse[queue[head]] {
			if reachable[source] && !canReachEnd[source] {
				canReachEnd[source] = true
				queue = append(queue, source)
			}
		}
	}

	indegree := map[string]int{}
	for id := range nodes {
		if reachable[id] && canReachEnd[id] {
			indegree[id] = 0
		}
	}
	if len(indegree) == 0 {
		return nil, 0, false
	}
	for source, targets := range adj {
		if _, relevant := indegree[source]; !relevant {
			continue
		}
		for _, target := range targets {
			if _, relevant := indegree[target]; relevant {
				indegree[target]++
			}
		}
	}
	topological := []string{}
	for id, degree := range indegree {
		if degree == 0 {
			topological = append(topological, id)
		}
	}
	sort.Strings(topological)
	order := make([]string, 0, len(indegree))
	for head := 0; head < len(topological); head++ {
		id := topological[head]
		order = append(order, id)
		for _, target := range adj[id] {
			if _, relevant := indegree[target]; !relevant {
				continue
			}
			indegree[target]--
			if indegree[target] == 0 {
				topological = append(topological, target)
			}
		}
	}
	if len(order) != len(indegree) {
		return nil, 0, false
	}

	distance := map[string]int64{}
	previous := map[string]string{}
	for _, trigger := range triggers {
		if _, relevant := indegree[trigger]; relevant {
			distance[trigger] = effectiveDuration(nodes[trigger])
		}
	}
	for _, id := range order {
		base, reached := distance[id]
		if !reached {
			continue
		}
		for _, target := range adj[id] {
			if _, relevant := indegree[target]; !relevant {
				continue
			}
			candidate := base + effectiveDuration(nodes[target])
			current, targetReached := distance[target]
			if !targetReached || candidate > current {
				distance[target] = candidate
				previous[target] = id
			}
		}
	}

	bestID := ""
	var best int64
	for id, node := range nodes {
		if node.Type != domain.NodeEnd || !canReachEnd[id] {
			continue
		}
		value, reached := distance[id]
		if reached && (bestID == "" || value > best || value == best && id < bestID) {
			best, bestID = value, id
		}
	}
	if bestID == "" {
		return nil, 0, false
	}
	path := []string{}
	for id := bestID; id != ""; id = previous[id] {
		path = append(path, id)
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	return path, best, true
}
