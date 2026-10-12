package workspace

import (
	"strings"
	"testing"

	"buildy/pkg/util"
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

	order, err := graph.Order()
	if err == nil {
		t.Error("Order should return an error for circular dependencies")
	}
	if len(order) > 0 {
		t.Error("Should not return an order when circular dependency exists")
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

	order, err := graph.Order()
	if err != nil {
		t.Fatalf("Order failed: %v", err)
	}
	position := make(map[string]int)
	for i, taskID := range order {
		position[taskID] = i
	}

	// Verify ordering constraints
	assertBefore := func(before, after string) {
		if position[before] >= position[after] {
			t.Errorf("%s (%d) should execute before %s (%d)", before, position[before], after, position[after])
		}
	}

	assertBefore("setup_001", "compile_001")
	assertBefore("setup_001", "compile_002")
	assertBefore("compile_001", "link_001")
	assertBefore("compile_002", "link_001")
	assertBefore("link_001", "stage_001")
}

// TestTaskGraphEmptyGraph handles edge case of empty task graph.
func TestTaskGraphEmptyGraph(t *testing.T) {
	graph := NewTaskGraph()

	order, err := graph.Order()
	if err != nil {
		t.Errorf("Empty graph should not error: %v", err)
	}
	if len(order) != 0 {
		t.Errorf("Empty graph should have no tasks, got %v", order)
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

	order, err := graph.Order()
	if err != nil {
		t.Fatalf("Single task graph should not error: %v", err)
	}
	if len(order) != 1 || order[0] != "single_task" {
		t.Errorf("order = %v, want [single_task]", order)
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

	_, err := graph.Order()
	if err == nil || !strings.Contains(err.Error(), "nonexistent_task") {
		t.Errorf("Should error naming the missing dependency, got %v", err)
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

	_, err := graph.Order()
	if err != nil {
		t.Fatalf("Order failed: %v", err)
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
	order, err := graph.Order()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "generate_version,compile_version,link_app" {
		t.Errorf("order = %v, want generate, compile, link", order)
	}
}

func TestValidateRejectsUnresolvedVariables(t *testing.T) {
	task := &BuildTask{TaskID: "compile_main", Command: commandLine("g++ -std=${std} -c main.cpp"), Outputs: []string{"/b/main.o"}}
	err := graphOf(t, task).Validate()
	if err == nil || !strings.Contains(err.Error(), "${std}") {
		t.Fatalf("got %v", err)
	}

	// An escaped \${...} is not a variable (a sed pattern in a shell tool's script).
	sed := &BuildTask{TaskID: "generate_config", Command: util.Command{Script: `sed 's/\${VERSION}/1.0/g' in > out`}, Outputs: []string{"/b/out"}}
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

// Finding 8 and S10: --target runs the target and its dependencies, in any module, and nothing
// else; an unknown target is an error.
func TestSelectTakesTheDependencyClosure(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"buildy.yaml":        "project: {name: ws}\n" + cppEnvironment + "workspace:\n  modules: [libmod, appmod]\n",
		"libmod/buildy.yaml": "project: {name: libmod}\ntargets:\n  static_libraries:\n    - {name: calc, language: cpp, sources: [add.cpp]}\n",
		"appmod/buildy.yaml": "project: {name: appmod}\ntargets:\n  executables:\n    - {name: app, language: cpp, sources: [main.cpp], libs: [calc]}\n    - {name: other, language: cpp, sources: [other.cpp]}\n",
		"libmod/add.cpp":     "int add(int a, int b) { return a + b; }",
		"appmod/main.cpp":    "int main() {}",
		"appmod/other.cpp":   "int main() {}",
	})
	generated, err := generate(t, root, "linux", "x86_64", "debug")
	if err != nil {
		t.Fatal(err)
	}
	graph := graphOf(t, generated.Tasks...)
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
	selected, err := graph.Select([]string{"app"}, generated.Targets)
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]bool{}
	for _, task := range selected.Tasks {
		targets[task.Target] = true
	}
	if !targets["app"] || !targets["calc"] || targets["other"] || len(targets) != 2 {
		t.Errorf("selected the tasks of %v, want app and calc", targets)
	}
	if _, err := selected.Order(); err != nil {
		t.Errorf("the selection is not a closed graph: %v", err)
	}

	_, err = graph.Select([]string{"nosuch"}, generated.Targets)
	if err == nil || !strings.Contains(err.Error(), "unknown target 'nosuch'") || !strings.Contains(err.Error(), "calc") {
		t.Errorf("got %v", err)
	}
}
