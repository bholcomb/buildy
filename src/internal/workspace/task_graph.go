package workspace

import (
	"container/heap"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"buildy/pkg/util"
)

// TaskGraph is the tasks of a build and their dependencies
type TaskGraph struct {
	Tasks map[string]*BuildTask
}

// NewTaskGraph creates a new TaskGraph
func NewTaskGraph() *TaskGraph {
	return &TaskGraph{Tasks: make(map[string]*BuildTask)}
}

// AddTask adds a task to the graph. Task IDs are unique.
func (tg *TaskGraph) AddTask(task *BuildTask) error {
	if existing, ok := tg.Tasks[task.TaskID]; ok {
		return fmt.Errorf("two tasks have the ID %s: %s and %s", task.TaskID, existing.Describe(), task.Describe())
	}
	tg.Tasks[task.TaskID] = task
	return nil
}

// unresolvedVariable matches a ${...} reference that is not escaped as \${...}.
var unresolvedVariable = regexp.MustCompile(`(^|[^\\])(\$\{[^}]*\})`)

// Validate checks the graph before anything runs, under --dry-run too, and adds the edges that
// follow from data. Each failure names the tasks and targets involved.
//
//  1. Every output has exactly one producer.
//  2. If a task's input is another task's output, the task depends on that producer.
//  3. Every dependency names a task in the graph.
//  4. No ${...} is left unresolved in a command, input or output.
//  5. Every task has its build context: a platform, an architecture and a configuration.
func (tg *TaskGraph) Validate() error {
	var problems []string

	producers := make(map[string]*BuildTask)
	for _, id := range tg.sortedIDs() {
		task := tg.Tasks[id]
		for _, output := range task.Outputs {
			key := util.NormalizePath(output)
			if other, ok := producers[key]; ok && other != task {
				problems = append(problems, fmt.Sprintf("output %s is produced by both %s and %s",
					output, other.Describe(), task.Describe()))
				continue
			}
			producers[key] = task
		}
	}

	for _, id := range tg.sortedIDs() {
		task := tg.Tasks[id]
		if task.Platform == "" || task.Architecture == "" || task.Configuration == "" {
			problems = append(problems, fmt.Sprintf("internal error: %s has no build context (platform '%s', architecture '%s', configuration '%s')",
				task.Describe(), task.Platform, task.Architecture, task.Configuration))
		}
		for _, input := range task.Inputs {
			producer, ok := producers[util.NormalizePath(input.Path)]
			if ok && producer != task && !hasString(task.Dependencies, producer.TaskID) {
				task.Dependencies = append(task.Dependencies, producer.TaskID)
			}
		}
		for _, dep := range task.Dependencies {
			if _, ok := tg.Tasks[dep]; !ok {
				problems = append(problems, fmt.Sprintf("%s depends on %s, which is not a task", task.Describe(), dep))
			}
		}
		check := func(what, text string) {
			if m := unresolvedVariable.FindStringSubmatch(text); m != nil {
				problems = append(problems, fmt.Sprintf("%s: unresolved variable %s in %s: %s", task.Describe(), m[2], what, text))
			}
		}
		for _, arg := range task.Command.Args {
			check("command", arg)
		}
		check("command", task.Command.Script)
		for _, input := range task.Inputs {
			check("input", input.Path)
		}
		for _, output := range task.Outputs {
			check("output", output)
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid task graph:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// Select returns the graph of what building the named targets takes: each target's final task
// (its link or build; finalTasks maps target names to them, Generated.Targets) and every task it
// transitively depends on. Validate the full graph first: the producer edges it adds are what make
// the closure complete. An unknown name is an error.
func (tg *TaskGraph) Select(targets []string, finalTasks map[string]string) (*TaskGraph, error) {
	selected := NewTaskGraph()
	var visit func(id string)
	visit = func(id string) {
		if _, done := selected.Tasks[id]; done {
			return
		}
		task := tg.Tasks[id]
		selected.Tasks[id] = task
		for _, dep := range task.Dependencies {
			visit(dep)
		}
	}
	for _, target := range targets {
		id, ok := finalTasks[target]
		if !ok || tg.Tasks[id] == nil {
			names := make([]string, 0, len(finalTasks))
			for name := range finalTasks {
				names = append(names, name)
			}
			sort.Strings(names)
			return nil, fmt.Errorf("unknown target '%s'; the targets are: %s", target, strings.Join(names, ", "))
		}
		visit(id)
	}
	return selected, nil
}

func (tg *TaskGraph) sortedIDs() []string {
	ids := make([]string, 0, len(tg.Tasks))
	for id := range tg.Tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func hasString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// Order returns the tasks in an order that runs each after its dependencies: of the tasks whose
// dependencies are done, the first by ID comes first. It numbers the tasks in that order (1-based),
// for logs, and fails on a cycle, naming the tasks in it or waiting on it.
func (tg *TaskGraph) Order() ([]string, error) {
	waiting := make(map[string]int, len(tg.Tasks))         // task -> dependencies not yet ordered
	dependents := make(map[string][]string, len(tg.Tasks)) // task -> the tasks that depend on it
	for _, id := range tg.sortedIDs() {
		deps := uniqueStrings(tg.Tasks[id].Dependencies)
		waiting[id] = len(deps)
		for _, dep := range deps {
			if tg.Tasks[dep] == nil {
				return nil, fmt.Errorf("%s depends on %s, which is not a task", tg.Tasks[id].Describe(), dep)
			}
			dependents[dep] = append(dependents[dep], id)
		}
	}

	ready := &IDQueue{}
	for _, id := range tg.sortedIDs() {
		if waiting[id] == 0 {
			ready.PushID(id)
		}
	}
	order := make([]string, 0, len(tg.Tasks))
	for ready.Len() > 0 {
		id := ready.PopID()
		order = append(order, id)
		for _, dependent := range dependents[id] {
			if waiting[dependent]--; waiting[dependent] == 0 {
				ready.PushID(dependent)
			}
		}
	}
	if len(order) < len(tg.Tasks) {
		var stuck []string
		for _, id := range tg.sortedIDs() {
			if waiting[id] > 0 {
				stuck = append(stuck, id)
			}
		}
		return nil, fmt.Errorf("circular dependency detected in tasks: %v", stuck)
	}
	for i, id := range order {
		tg.Tasks[id].TaskNumber = i + 1
	}
	return order, nil
}

// uniqueStrings returns list without repeats, in order.
func uniqueStrings(list []string) []string {
	seen := make(map[string]bool, len(list))
	result := make([]string, 0, len(list))
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// IDQueue is a priority queue of task IDs, smallest first (container/heap).
type IDQueue []string

func (q IDQueue) Len() int           { return len(q) }
func (q IDQueue) Less(i, j int) bool { return q[i] < q[j] }
func (q IDQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *IDQueue) Push(x any)        { *q = append(*q, x.(string)) }
func (q *IDQueue) Pop() any {
	old := *q
	x := old[len(old)-1]
	*q = old[:len(old)-1]
	return x
}

// PushID adds an ID.
func (q *IDQueue) PushID(id string) { heap.Push(q, id) }

// PopID removes and returns the smallest ID.
func (q *IDQueue) PopID() string { return heap.Pop(q).(string) }
