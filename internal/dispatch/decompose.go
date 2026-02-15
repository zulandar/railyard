package dispatch

import (
	"fmt"
	"strings"
)

// BeadPlan represents a planned bead in a decomposition.
type BeadPlan struct {
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
	BeadID    string
	BlockedBy string
}

// DecompositionPlan represents the full output of a dispatch decomposition.
type DecompositionPlan struct {
	Beads []BeadPlan
	Deps  []DepPlan
}

// ValidatePlan checks that a decomposition plan is structurally valid.
// Returns a list of validation errors (empty if valid).
func ValidatePlan(plan *DecompositionPlan) []string {
	if plan == nil {
		return []string{"plan is nil"}
	}

	var errs []string

	if len(plan.Beads) == 0 {
		errs = append(errs, "plan has no beads")
	}

	ids := make(map[string]bool)
	for i, b := range plan.Beads {
		if b.ID == "" {
			errs = append(errs, fmt.Sprintf("beads[%d]: ID is required", i))
		}
		if b.Title == "" {
			errs = append(errs, fmt.Sprintf("beads[%d] (%s): title is required", i, b.ID))
		}
		if b.Track == "" {
			errs = append(errs, fmt.Sprintf("beads[%d] (%s): track is required", i, b.ID))
		}
		if b.Type == "" {
			errs = append(errs, fmt.Sprintf("beads[%d] (%s): type is required", i, b.ID))
		}
		if b.Type != "" && b.Type != "epic" && b.Type != "task" && b.Type != "spike" {
			errs = append(errs, fmt.Sprintf("beads[%d] (%s): invalid type %q (must be epic, task, or spike)", i, b.ID, b.Type))
		}
		if b.Acceptance == "" {
			errs = append(errs, fmt.Sprintf("beads[%d] (%s): acceptance criteria required", i, b.ID))
		}
		if b.Priority < 0 || b.Priority > 4 {
			errs = append(errs, fmt.Sprintf("beads[%d] (%s): priority %d out of range 0-4", i, b.ID, b.Priority))
		}
		if ids[b.ID] {
			errs = append(errs, fmt.Sprintf("beads[%d]: duplicate ID %q", i, b.ID))
		}
		ids[b.ID] = true

		// Validate parent reference
		if b.ParentID != "" && !ids[b.ParentID] {
			// Parent may appear later — we'll check at the end
		}
	}

	// Validate parent references (after collecting all IDs)
	for i, b := range plan.Beads {
		if b.ParentID != "" && !ids[b.ParentID] {
			errs = append(errs, fmt.Sprintf("beads[%d] (%s): parent %q not found in plan", i, b.ID, b.ParentID))
		}
	}

	// Validate dependency references
	for i, d := range plan.Deps {
		if d.BeadID == "" {
			errs = append(errs, fmt.Sprintf("deps[%d]: bead_id is required", i))
		}
		if d.BlockedBy == "" {
			errs = append(errs, fmt.Sprintf("deps[%d]: blocked_by is required", i))
		}
		if d.BeadID != "" && !ids[d.BeadID] {
			errs = append(errs, fmt.Sprintf("deps[%d]: bead %q not found in plan", i, d.BeadID))
		}
		if d.BlockedBy != "" && !ids[d.BlockedBy] {
			errs = append(errs, fmt.Sprintf("deps[%d]: blocked_by %q not found in plan", i, d.BlockedBy))
		}
		if d.BeadID == d.BlockedBy {
			errs = append(errs, fmt.Sprintf("deps[%d]: bead %q cannot depend on itself", i, d.BeadID))
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
	// Build adjacency list: bead -> [things it's blocked by]
	graph := make(map[string][]string)
	for _, d := range deps {
		graph[d.BeadID] = append(graph[d.BeadID], d.BlockedBy)
	}

	// Collect all nodes
	nodes := make(map[string]bool)
	for _, d := range deps {
		nodes[d.BeadID] = true
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

// TrackSummary generates a summary of beads organized by track.
func TrackSummary(plan *DecompositionPlan) map[string][]BeadPlan {
	result := make(map[string][]BeadPlan)
	for _, b := range plan.Beads {
		result[b.Track] = append(result[b.Track], b)
	}
	return result
}
