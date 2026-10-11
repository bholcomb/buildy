package workspace

import (
	"strings"
	"testing"
)

// TestTaskGraphCircularDependencyDetection ensures the task graph detects
// circular dependencies and returns an error rather than hanging.
func TestTaskGraphCircularDependencyDetection(t *testing.T) {
	graph := NewTaskGraph()

	// Create tasks with circular dependency: A → B → C → A
	taskA := &BuildTask{
		TaskID:       "task_a",
		TaskType:     "compile",
		Dependencies: []string{"task_c"}, // A depends on C (circular!)
	}
	taskB := &BuildTask{
		TaskID:       "task_b",
		TaskType:     "compile",
		Dependencies: []string{"task_a"}, // B depends on A
	}
	taskC := &BuildTask{
		TaskID:       "task_c",
		TaskType:     "compile",
		Dependencies: []string{"task_b"}, // C depends on B
	}

	graph.AddTask(taskA)
	graph.AddTask(taskB)
	graph.AddTask(taskC)

	stages, err := graph.BuildExecutionStages()

	if err == nil {
		t.Error("BuildExecutionStages should return an error for circular dependencies")
	}

	if stages != nil && len(stages) > 0 {
		t.Error("Should not return stages when circular dependency exists")
	}

	t.Logf("Correctly detected circular dependency: %v", err)
}

// TestTaskGraphTopologicalOrder ensures tasks execute in correct dependency order.
func TestTaskGraphTopologicalOrder(t *testing.T) {
	graph := NewTaskGraph()

	// Create a dependency chain: setup → compile1, compile2 → link → stage
	setup := &BuildTask{
		TaskID:       "setup_001",
		TaskType:     "setup",
		Dependencies: []string{},
	}
	compile1 := &BuildTask{
		TaskID:       "compile_001",
		TaskType:     "compile",
		Dependencies: []string{"setup_001"},
	}
	compile2 := &BuildTask{
		TaskID:       "compile_002",
		TaskType:     "compile",
		Dependencies: []string{"setup_001"},
	}
	link := &BuildTask{
		TaskID:       "link_001",
		TaskType:     "link",
		Dependencies: []string{"compile_001", "compile_002"},
	}
	stage := &BuildTask{
		TaskID:       "stage_001",
		TaskType:     "stage",
		Dependencies: []string{"link_001"},
	}

	graph.AddTask(setup)
	graph.AddTask(compile1)
	graph.AddTask(compile2)
	graph.AddTask(link)
	graph.AddTask(stage)

	stages, err := graph.BuildExecutionStages()
	if err != nil {
		t.Fatalf("BuildExecutionStages failed: %v", err)
	}

	if len(stages) == 0 {
		t.Fatal("Expected at least one execution stage")
	}

	// Build a map of task → stage index for verification
	taskStage := make(map[string]int)
	for stageIdx, stage := range stages {
		for _, taskID := range stage {
			taskStage[taskID] = stageIdx
		}
	}

	// Verify ordering constraints
	assertBefore := func(before, after string) {
		if taskStage[before] >= taskStage[after] {
			t.Errorf("%s (stage %d) should execute before %s (stage %d)",
				before, taskStage[before], after, taskStage[after])
		}
	}

	assertBefore("setup_001", "compile_001")
	assertBefore("setup_001", "compile_002")
	assertBefore("compile_001", "link_001")
	assertBefore("compile_002", "link_001")
	assertBefore("link_001", "stage_001")

	// Compile tasks can be parallel (same stage is ok)
	if taskStage["compile_001"] != taskStage["compile_002"] {
		t.Logf("Note: compile_001 and compile_002 are in different stages (%d vs %d), but parallel is preferred",
			taskStage["compile_001"], taskStage["compile_002"])
	}

	t.Logf("SUCCESS: Tasks correctly ordered across %d stages", len(stages))
}

// TestTaskGraphEmptyGraph handles edge case of empty task graph.
func TestTaskGraphEmptyGraph(t *testing.T) {
	graph := NewTaskGraph()

	stages, err := graph.BuildExecutionStages()
	if err != nil {
		t.Errorf("Empty graph should not error: %v", err)
	}

	if len(stages) != 0 {
		t.Errorf("Empty graph should have 0 stages, got %d", len(stages))
	}
}

// TestTaskGraphSingleTask handles single task with no dependencies.
func TestTaskGraphSingleTask(t *testing.T) {
	graph := NewTaskGraph()

	task := &BuildTask{
		TaskID:       "single_task",
		TaskType:     "build",
		Dependencies: []string{},
	}
	graph.AddTask(task)

	stages, err := graph.BuildExecutionStages()
	if err != nil {
		t.Fatalf("Single task graph should not error: %v", err)
	}

	if len(stages) != 1 {
		t.Errorf("Expected 1 stage for single task, got %d", len(stages))
	}

	if len(stages[0]) != 1 || stages[0][0] != "single_task" {
		t.Errorf("Expected single_task in first stage")
	}
}

// TestTaskGraphMissingDependency ensures missing dependencies are handled.
func TestTaskGraphMissingDependency(t *testing.T) {
	graph := NewTaskGraph()

	task := &BuildTask{
		TaskID:       "task_with_missing_dep",
		TaskType:     "link",
		Dependencies: []string{"nonexistent_task"},
	}
	graph.AddTask(task)

	stages, err := graph.BuildExecutionStages()

	// Missing dependencies should be detected
	if err == nil {
		t.Error("Should error when dependency doesn't exist")
	}

	if stages != nil && len(stages) > 0 {
		t.Error("Should not return stages when dependency is missing")
	}
}

// TestTaskNumbersAssigned verifies task numbers are assigned in execution order.
func TestTaskNumbersAssigned(t *testing.T) {
	graph := NewTaskGraph()

	// Create simple chain
	task1 := &BuildTask{TaskID: "first", Dependencies: []string{}}
	task2 := &BuildTask{TaskID: "second", Dependencies: []string{"first"}}
	task3 := &BuildTask{TaskID: "third", Dependencies: []string{"second"}}

	graph.AddTask(task1)
	graph.AddTask(task2)
	graph.AddTask(task3)

	_, err := graph.BuildExecutionStages()
	if err != nil {
		t.Fatalf("BuildExecutionStages failed: %v", err)
	}

	// Verify task numbers are assigned
	if task1.TaskNumber == 0 {
		t.Error("task1 should have a non-zero TaskNumber")
	}
	if task2.TaskNumber == 0 {
		t.Error("task2 should have a non-zero TaskNumber")
	}
	if task3.TaskNumber == 0 {
		t.Error("task3 should have a non-zero TaskNumber")
	}

	// Verify ordering
	if task1.TaskNumber >= task2.TaskNumber {
		t.Errorf("task1 (%d) should have lower number than task2 (%d)",
			task1.TaskNumber, task2.TaskNumber)
	}
	if task2.TaskNumber >= task3.TaskNumber {
		t.Errorf("task2 (%d) should have lower number than task3 (%d)",
			task2.TaskNumber, task3.TaskNumber)
	}

	t.Logf("SUCCESS: Task numbers assigned: %d, %d, %d",
		task1.TaskNumber, task2.TaskNumber, task3.TaskNumber)
}

func graphOf(t *testing.T, tasks ...*BuildTask) *TaskGraph {
	t.Helper()
	graph := NewTaskGraph()
	for _, task := range tasks {
		if err := graph.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}
	return graph
}

// Findings 2 and S4: two tasks declaring one output are rejected, both named.
func TestValidateRejectsTwoProducersOfOneOutput(t *testing.T) {
	one := &BuildTask{TaskID: "compile_one_main_cpp", Module: ".", Target: "one", Outputs: []string{"/b/obj/main.o"}}
	two := &BuildTask{TaskID: "compile_two_main_cpp", Module: ".", Target: "two", Outputs: []string{"/b/obj/main.o"}}
	err := graphOf(t, one, two).Validate()
	if err == nil {
		t.Fatal("two producers of one output were accepted")
	}
	for _, want := range []string{"/b/obj/main.o", "compile_one_main_cpp (module ., target one)", "compile_two_main_cpp (module ., target two)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestValidateRejectsUnknownDependency(t *testing.T) {
	task := &BuildTask{TaskID: "link_app", Module: "appmod", Target: "app", Dependencies: []string{"link_nosuch"}}
	err := graphOf(t, task).Validate()
	if err == nil || !strings.Contains(err.Error(), "link_app (module appmod, target app) depends on link_nosuch") {
		t.Fatalf("got %v", err)
	}
}

// S15: a task reading another task's output depends on it, without anyone declaring the edge.
func TestValidateAddsProducerEdges(t *testing.T) {
	gen := &BuildTask{TaskID: "generate_version", Outputs: []string{"/b/gen/version.cpp"}}
	compile := &BuildTask{TaskID: "compile_version", Inputs: []TaskInput{{Path: "/b/gen/version.cpp"}}, Outputs: []string{"/b/obj/version.o"}}
	link := &BuildTask{TaskID: "link_app", Inputs: []TaskInput{{Path: "/b/obj/version.o"}}, Outputs: []string{"/b/bin/app"}, Dependencies: []string{"compile_version"}}
	graph := graphOf(t, gen, compile, link)
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(compile.Dependencies, ",") != "generate_version" {
		t.Errorf("compile dependencies = %v, want [generate_version]", compile.Dependencies)
	}
	if strings.Join(link.Dependencies, ",") != "compile_version" {
		t.Errorf("link dependencies = %v, want [compile_version] once", link.Dependencies)
	}
	stages, err := graph.BuildExecutionStages()
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 3 {
		t.Errorf("stages = %v, want generate, compile, link", stages)
	}
}

func TestValidateRejectsUnresolvedVariables(t *testing.T) {
	task := &BuildTask{TaskID: "compile_main", Command: "g++ -std=${std} -c main.cpp", Outputs: []string{"/b/main.o"}}
	err := graphOf(t, task).Validate()
	if err == nil || !strings.Contains(err.Error(), "${std}") {
		t.Fatalf("got %v", err)
	}

	// An escaped \${...} is not a variable (sed patterns in generate tasks).
	sed := &BuildTask{TaskID: "generate_config", Command: `sed 's/\${VERSION}/1.0/g' in > out`, Outputs: []string{"/b/out"}}
	if err := graphOf(t, sed).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAddTaskRejectsDuplicateIDs(t *testing.T) {
	graph := NewTaskGraph()
	if err := graph.AddTask(&BuildTask{TaskID: "x", Module: "a_b"}); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddTask(&BuildTask{TaskID: "x", Module: "a/b"}); err == nil {
		t.Fatal("a duplicate task ID was accepted")
	}
}
