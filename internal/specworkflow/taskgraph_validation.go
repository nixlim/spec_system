package specworkflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// kebabCaseRe matches a valid kebab-case identifier: lowercase alphanumeric
// segments separated by single hyphens.
var kebabCaseRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// validPriorities lists the accepted priority values (stored lowercase).
var validPriorities = map[string]bool{
	"critical": true,
	"high":     true,
	"medium":   true,
	"low":      true,
}

// TaskGraphFile is the top-level structure of a task graph JSON file.
type TaskGraphFile struct {
	Version string          `json:"version"`
	Tasks   []TaskGraphTask `json:"tasks"`
}

// TaskGraphTask represents a single task within a task graph.
type TaskGraphTask struct {
	TaskID     string          `json:"task_id"`
	TaskName   string          `json:"task_name"`
	Goal       string          `json:"goal"`
	Acceptance []string        `json:"acceptance"`
	DependsOn  json.RawMessage `json:"depends_on,omitempty"`
	Priority   string          `json:"priority"`
	// Additional fields are allowed but not validated beyond the required set.
}

// parseDependsOn extracts the list of dependency task IDs from the depends_on
// field. It handles three cases:
//   - JSON array of strings → returned as-is
//   - JSON object (e.g. {"status":"N/A","reason":"..."}) → returns nil (no deps)
//   - nil / absent → returns nil
func parseDependsOn(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	// Try array first.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}

	// Try object (treated as "no dependencies").
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return nil, nil
	}

	return nil, fmt.Errorf("depends_on must be an array of strings or an object, got: %s", string(raw))
}

// ValidateTaskGraph validates a task graph JSON document and returns a list of
// validation errors. An empty slice means the graph is valid.
//
// Checks performed:
//  1. version field is present and non-empty
//  2. tasks array is non-empty
//  3. Each task_id is kebab-case
//  4. No duplicate task_ids
//  5. Required fields are non-empty: task_name, goal, acceptance (≥1 item)
//  6. Priority is one of: critical, high, medium, low (case-insensitive)
//  7. DAG: no self-references in depends_on
//  8. DAG: no dangling depends_on references
//  9. DAG: no cycles
func ValidateTaskGraph(data []byte) []string {
	var errs []string

	var graph TaskGraphFile
	if err := json.Unmarshal(data, &graph); err != nil {
		return []string{fmt.Sprintf("invalid JSON: %v", err)}
	}

	// 1. Version required.
	if strings.TrimSpace(graph.Version) == "" {
		errs = append(errs, "version required")
	}

	// 2. Non-empty tasks.
	if len(graph.Tasks) == 0 {
		errs = append(errs, "at least one task required")
		return errs // no point continuing without tasks
	}

	// Build ID set for duplicate and reference checks.
	taskIDs := make(map[string]bool, len(graph.Tasks))
	// adjacency stores depends_on edges for cycle detection.
	adjacency := make(map[string][]string, len(graph.Tasks))

	for i, t := range graph.Tasks {
		label := fmt.Sprintf("task[%d]", i)
		if t.TaskID != "" {
			label = fmt.Sprintf("task %q", t.TaskID)
		}

		// 3. Kebab-case ID.
		if t.TaskID == "" {
			errs = append(errs, fmt.Sprintf("%s: task_id required", label))
		} else if !kebabCaseRe.MatchString(t.TaskID) {
			errs = append(errs, fmt.Sprintf("%s: task_id must be kebab-case", label))
		}

		// 4. Duplicate check.
		if t.TaskID != "" {
			if taskIDs[t.TaskID] {
				errs = append(errs, fmt.Sprintf("duplicate task_id %q", t.TaskID))
			}
			taskIDs[t.TaskID] = true
		}

		// 5. Required fields.
		if strings.TrimSpace(t.TaskName) == "" {
			errs = append(errs, fmt.Sprintf("%s: task_name required", label))
		}
		if strings.TrimSpace(t.Goal) == "" {
			errs = append(errs, fmt.Sprintf("%s: goal required", label))
		}
		if len(t.Acceptance) == 0 {
			errs = append(errs, fmt.Sprintf("%s: at least one acceptance criterion required", label))
		}

		// 6. Priority.
		if strings.TrimSpace(t.Priority) == "" {
			errs = append(errs, fmt.Sprintf("%s: priority required", label))
		} else if !validPriorities[strings.ToLower(strings.TrimSpace(t.Priority))] {
			errs = append(errs, fmt.Sprintf("%s: invalid priority %q, must be one of: critical, high, medium, low", label, t.Priority))
		}

		// Parse depends_on for DAG checks.
		deps, err := parseDependsOn(t.DependsOn)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", label, err))
			continue
		}

		for _, dep := range deps {
			// 7. Self-reference.
			if dep == t.TaskID {
				errs = append(errs, fmt.Sprintf("%s: task depends on itself", label))
			}
		}
		if t.TaskID != "" {
			adjacency[t.TaskID] = deps
		}
	}

	// 8. Dangling references.
	for id, deps := range adjacency {
		for _, dep := range deps {
			if !taskIDs[dep] {
				errs = append(errs, fmt.Sprintf("task %q: depends_on references non-existent task %q", id, dep))
			}
		}
	}

	// 9. Cycle detection via DFS with coloring.
	errs = append(errs, detectCycles(adjacency)...)

	return errs
}

// detectCycles performs a DFS-based cycle detection on the dependency graph.
// Returns a list of error strings, one per cycle found.
func detectCycles(adj map[string][]string) []string {
	const (
		white = 0 // unvisited
		gray  = 1 // in current DFS path
		black = 2 // fully processed
	)

	color := make(map[string]int, len(adj))
	var errs []string

	var visit func(node string) bool
	visit = func(node string) bool {
		color[node] = gray
		for _, dep := range adj[node] {
			switch color[dep] {
			case gray:
				errs = append(errs, "cycle detected in depends_on")
				return true
			case white:
				if visit(dep) {
					return true
				}
			}
		}
		color[node] = black
		return false
	}

	for node := range adj {
		if color[node] == white {
			visit(node)
		}
	}

	return errs
}
