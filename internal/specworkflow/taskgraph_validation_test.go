package specworkflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// helper builds a valid task graph and returns it as JSON bytes.
func validTaskGraph() TaskGraphFile {
	return TaskGraphFile{
		Version: "0.1.0",
		Tasks: []TaskGraphTask{
			{
				TaskID:     "setup-database",
				TaskName:   "Set up database",
				Goal:       "Create the database schema",
				Acceptance: []string{"Schema is created", "Migrations run"},
				Priority:   "high",
			},
			{
				TaskID:     "build-api",
				TaskName:   "Build API layer",
				Goal:       "Implement REST API endpoints",
				Acceptance: []string{"Endpoints respond correctly"},
				DependsOn:  json.RawMessage(`["setup-database"]`),
				Priority:   "medium",
			},
			{
				TaskID:     "write-tests",
				TaskName:   "Write integration tests",
				Goal:       "Full test coverage for API",
				Acceptance: []string{"All tests pass"},
				DependsOn:  json.RawMessage(`["build-api"]`),
				Priority:   "low",
			},
		},
	}
}

func marshal(t *testing.T, g TaskGraphFile) []byte {
	t.Helper()
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("failed to marshal task graph: %v", err)
	}
	return data
}

// --- Dataset tests 1-14 from spec ---

func TestValidateTaskGraph_ValidGraph(t *testing.T) {
	// Dataset #1: Valid graph with version, 3 tasks, all required fields, valid DAG
	errs := ValidateTaskGraph(marshal(t, validTaskGraph()))
	if len(errs) != 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidateTaskGraph_MissingVersion(t *testing.T) {
	// Dataset #2: Missing version field
	g := validTaskGraph()
	g.Version = ""
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "version required")
}

func TestValidateTaskGraph_EmptyTasks(t *testing.T) {
	// Dataset #3: Empty tasks array
	g := TaskGraphFile{Version: "1.0", Tasks: []TaskGraphTask{}}
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "at least one task required")
}

func TestValidateTaskGraph_NonKebabCaseID(t *testing.T) {
	// Dataset #4: Non-kebab-case task_id
	g := validTaskGraph()
	g.Tasks = []TaskGraphTask{{
		TaskID:     "MyTask",
		TaskName:   "My Task",
		Goal:       "Do something",
		Acceptance: []string{"Done"},
		Priority:   "high",
	}}
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "task_id must be kebab-case")
}

func TestValidateTaskGraph_DuplicateIDs(t *testing.T) {
	// Dataset #5: Duplicate task_ids
	g := validTaskGraph()
	g.Tasks = append(g.Tasks, TaskGraphTask{
		TaskID:     "setup-database",
		TaskName:   "Duplicate",
		Goal:       "Duplicate task",
		Acceptance: []string{"Done"},
		Priority:   "high",
	})
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "duplicate task_id")
}

func TestValidateTaskGraph_MissingGoal(t *testing.T) {
	// Dataset #6: Task missing goal
	g := validTaskGraph()
	g.Tasks[0].Goal = ""
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "goal required")
}

func TestValidateTaskGraph_InvalidPriority(t *testing.T) {
	// Dataset #7: Invalid priority value
	g := validTaskGraph()
	g.Tasks[0].Priority = "urgent"
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "invalid priority")
	assertContainsError(t, errs, "critical, high, medium, low")
}

func TestValidateTaskGraph_EmptyAcceptance(t *testing.T) {
	// Dataset #8: Empty acceptance array
	g := validTaskGraph()
	g.Tasks[0].Acceptance = []string{}
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "at least one acceptance criterion required")
}

func TestValidateTaskGraph_MixedCasePriority(t *testing.T) {
	// Dataset #9: Valid priority with mixed case
	g := validTaskGraph()
	g.Tasks[0].Priority = "High"
	g.Tasks[1].Priority = "CRITICAL"
	g.Tasks[2].Priority = "Low"
	errs := ValidateTaskGraph(marshal(t, g))
	if len(errs) != 0 {
		t.Errorf("expected no errors for mixed-case priorities, got: %v", errs)
	}
}

func TestValidateTaskGraph_LargeGraph(t *testing.T) {
	// Dataset #10: Graph with 100 tasks
	g := TaskGraphFile{Version: "1.0"}
	for i := 0; i < 100; i++ {
		g.Tasks = append(g.Tasks, TaskGraphTask{
			TaskID:     kebabID(i),
			TaskName:   "Task",
			Goal:       "Goal",
			Acceptance: []string{"Done"},
			Priority:   "medium",
		})
	}
	errs := ValidateTaskGraph(marshal(t, g))
	if len(errs) != 0 {
		t.Errorf("expected no errors for 100-task graph, got: %v", errs)
	}
}

func TestValidateTaskGraph_TwoNodeCycle(t *testing.T) {
	// Dataset #11: task-a depends on task-b, task-b depends on task-a
	g := TaskGraphFile{
		Version: "1.0",
		Tasks: []TaskGraphTask{
			{
				TaskID:     "task-a",
				TaskName:   "Task A",
				Goal:       "Goal A",
				Acceptance: []string{"Done"},
				DependsOn:  json.RawMessage(`["task-b"]`),
				Priority:   "high",
			},
			{
				TaskID:     "task-b",
				TaskName:   "Task B",
				Goal:       "Goal B",
				Acceptance: []string{"Done"},
				DependsOn:  json.RawMessage(`["task-a"]`),
				Priority:   "high",
			},
		},
	}
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "cycle detected")
}

func TestValidateTaskGraph_DanglingReference(t *testing.T) {
	// Dataset #12: depends_on references non-existent task
	g := TaskGraphFile{
		Version: "1.0",
		Tasks: []TaskGraphTask{
			{
				TaskID:     "task-a",
				TaskName:   "Task A",
				Goal:       "Goal A",
				Acceptance: []string{"Done"},
				DependsOn:  json.RawMessage(`["non-existent-task"]`),
				Priority:   "high",
			},
		},
	}
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "depends_on references non-existent task")
}

func TestValidateTaskGraph_SelfReference(t *testing.T) {
	// Dataset #13: task depends on itself
	g := TaskGraphFile{
		Version: "1.0",
		Tasks: []TaskGraphTask{
			{
				TaskID:     "task-a",
				TaskName:   "Task A",
				Goal:       "Goal A",
				Acceptance: []string{"Done"},
				DependsOn:  json.RawMessage(`["task-a"]`),
				Priority:   "high",
			},
		},
	}
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "task depends on itself")
}

func TestValidateTaskGraph_ThreeNodeCycle(t *testing.T) {
	// Dataset #14: a→b→c→a
	g := TaskGraphFile{
		Version: "1.0",
		Tasks: []TaskGraphTask{
			{
				TaskID:     "task-a",
				TaskName:   "Task A",
				Goal:       "Goal A",
				Acceptance: []string{"Done"},
				DependsOn:  json.RawMessage(`["task-b"]`),
				Priority:   "high",
			},
			{
				TaskID:     "task-b",
				TaskName:   "Task B",
				Goal:       "Goal B",
				Acceptance: []string{"Done"},
				DependsOn:  json.RawMessage(`["task-c"]`),
				Priority:   "high",
			},
			{
				TaskID:     "task-c",
				TaskName:   "Task C",
				Goal:       "Goal C",
				Acceptance: []string{"Done"},
				DependsOn:  json.RawMessage(`["task-a"]`),
				Priority:   "high",
			},
		},
	}
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "cycle detected")
}

// --- Additional edge case tests ---

func TestValidateTaskGraph_InvalidJSON(t *testing.T) {
	errs := ValidateTaskGraph([]byte(`{not json`))
	assertContainsError(t, errs, "invalid JSON")
}

func TestValidateTaskGraph_ObjectDependsOn(t *testing.T) {
	// depends_on as object (N/A style) should be treated as no dependencies
	g := TaskGraphFile{
		Version: "1.0",
		Tasks: []TaskGraphTask{
			{
				TaskID:     "task-a",
				TaskName:   "Task A",
				Goal:       "Goal A",
				Acceptance: []string{"Done"},
				DependsOn:  json.RawMessage(`{"status":"N/A","reason":"no deps"}`),
				Priority:   "critical",
			},
		},
	}
	errs := ValidateTaskGraph(marshal(t, g))
	if len(errs) != 0 {
		t.Errorf("expected no errors for object depends_on, got: %v", errs)
	}
}

func TestValidateTaskGraph_MissingTaskName(t *testing.T) {
	g := validTaskGraph()
	g.Tasks[0].TaskName = ""
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "task_name required")
}

func TestValidateTaskGraph_MissingPriority(t *testing.T) {
	g := validTaskGraph()
	g.Tasks[0].Priority = ""
	errs := ValidateTaskGraph(marshal(t, g))
	assertContainsError(t, errs, "priority required")
}

// --- helpers ---

func assertContainsError(t *testing.T, errs []string, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return
		}
	}
	t.Errorf("expected an error containing %q, got: %v", substr, errs)
}

func kebabID(i int) string {
	return "task-" + strings.ReplaceAll(strings.ToLower(
		strings.TrimLeft(strings.Replace(
			strings.Replace(
				strings.Replace(
					string(rune('a'+i%26)),
					"", "", 0,
				), "", "", 0,
			), "", "", 0,
		), ""),
	), " ", "-") + "-" + itoa(i)
}

func itoa(i int) string {
	if i < 0 {
		return "-" + itoa(-i)
	}
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}
