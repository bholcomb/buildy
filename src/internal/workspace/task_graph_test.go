package workspace

import (
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
