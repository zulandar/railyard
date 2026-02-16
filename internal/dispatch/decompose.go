package dispatch

import (
	"fmt"
	"strings"
)

// CarPlan represents a planned car in a decomposition.
type CarPlan struct {
	ID          string
	Title       string
	Track       string
	Type        string // "epic", "task", "spike"
	Priority    int
	ParentID    string
	Description string
	Acceptance  string
}

// DepPlan represents a planned dependency.
type DepPlan struct {
	CarID    string
	BlockedBy string
}

// DecompositionPlan represents the full output of a dispatch decomposition.
type DecompositionPlan struct {
	Cars []CarPlan
	Deps  []DepPlan
}

// ValidatePlan checks that a decomposition plan is structurally valid.
// Returns a list of validation errors (empty if valid).
func ValidatePlan(plan *DecompositionPlan) []string {
	if plan == nil {
		return []string{"plan is nil"}
	}

	var errs []string

	if len(plan.Cars) == 0 {
		errs = append(errs, "plan has no cars")
	}

	ids := make(map[string]bool)
	for i, b := range plan.Cars {
		if b.ID == "" {
			errs = append(errs, fmt.Sprintf("cars[%d]: ID is required", i))
		}
		if b.Title == "" {
			errs = append(errs, fmt.Sprintf("cars[%d] (%s): title is required", i, b.ID))
		}
		if b.Track == "" {
			errs = append(errs, fmt.Sprintf("cars[%d] (%s): track is required", i, b.ID))
		}
		if b.Type == "" {
			errs = append(errs, fmt.Sprintf("cars[%d] (%s): type is required", i, b.ID))
		}
		if b.Type != "" && b.Type != "epic" && b.Type != "task" && b.Type != "spike" {
			errs = append(errs, fmt.Sprintf("cars[%d] (%s): invalid type %q (must be epic, task, or spike)", i, b.ID, b.Type))
		}
		if b.Acceptance == "" {
			errs = append(errs, fmt.Sprintf("cars[%d] (%s): acceptance criteria required", i, b.ID))
		}
		if b.Priority < 0 || b.Priority > 4 {
			errs = append(errs, fmt.Sprintf("cars[%d] (%s): priority %d out of range 0-4", i, b.ID, b.Priority))
		}
		if ids[b.ID] {
			errs = append(errs, fmt.Sprintf("cars[%d]: duplicate ID %q", i, b.ID))
		}
		ids[b.ID] = true

		// Validate parent reference
		if b.ParentID != "" && !ids[b.ParentID] {
			// Parent may appear later — we'll check at the end
		}
	}

	// Validate parent references (after collecting all IDs)
	for i, b := range plan.Cars {
		if b.ParentID != "" && !ids[b.ParentID] {
			errs = append(errs, fmt.Sprintf("cars[%d] (%s): parent %q not found in plan", i, b.ID, b.ParentID))
		}
	}

	// Validate dependency references
	for i, d := range plan.Deps {
		if d.CarID == "" {
			errs = append(errs, fmt.Sprintf("deps[%d]: car_id is required", i))
		}
		if d.BlockedBy == "" {
			errs = append(errs, fmt.Sprintf("deps[%d]: blocked_by is required", i))
		}
		if d.CarID != "" && !ids[d.CarID] {
			errs = append(errs, fmt.Sprintf("deps[%d]: car %q not found in plan", i, d.CarID))
		}
		if d.BlockedBy != "" && !ids[d.BlockedBy] {
			errs = append(errs, fmt.Sprintf("deps[%d]: blocked_by %q not found in plan", i, d.BlockedBy))
		}
		if d.CarID == d.BlockedBy {
			errs = append(errs, fmt.Sprintf("deps[%d]: car %q cannot depend on itself", i, d.CarID))
		}
	}

	// Check for cycles
	if cycle := DetectCycle(plan.Deps); cycle != nil {
		errs = append(errs, fmt.Sprintf("dependency cycle detected: %s", strings.Join(cycle, " → ")))
	}

	return errs
}

// DetectCycle checks for cycles in a dependency graph.
// Returns the cycle path if found, nil if no cycle.
func DetectCycle(deps []DepPlan) []string {
	// Build adjacency list: car -> [things it's blocked by]
	graph := make(map[string][]string)
	for _, d := range deps {
		graph[d.CarID] = append(graph[d.CarID], d.BlockedBy)
	}

	// Collect all nodes
	nodes := make(map[string]bool)
	for _, d := range deps {
		nodes[d.CarID] = true
		nodes[d.BlockedBy] = true
	}

	// DFS with coloring: 0=unvisited, 1=in-progress, 2=done
	color := make(map[string]int)
	parent := make(map[string]string)

	var dfs func(node string) []string
	dfs = func(node string) []string {
		color[node] = 1
		for _, next := range graph[node] {
			if color[next] == 1 {
				// Found cycle — reconstruct path
				path := []string{next, node}
				for cur := node; cur != next; {
					cur = parent[cur]
					if cur == "" {
						break
					}
					path = append(path, cur)
				}
				// Reverse
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				path = append(path, path[0]) // close the cycle
				return path
			}
			if color[next] == 0 {
				parent[next] = node
				if cycle := dfs(next); cycle != nil {
					return cycle
				}
			}
		}
		color[node] = 2
		return nil
	}

	for node := range nodes {
		if color[node] == 0 {
			if cycle := dfs(node); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}

// TrackSummary generates a summary of cars organized by track.
func TrackSummary(plan *DecompositionPlan) map[string][]CarPlan {
	result := make(map[string][]CarPlan)
	for _, b := range plan.Cars {
		result[b.Track] = append(result[b.Track], b)
	}
	return result
}
