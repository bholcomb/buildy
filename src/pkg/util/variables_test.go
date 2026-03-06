package util

import (
	"testing"
)

// TestVariableResolutionBasic tests simple variable substitution.
func TestVariableResolutionBasic(t *testing.T) {
	ve := NewVariableEnvironment(nil)
	ve.SetVariable("name", "world", "test")

	result := ve.ResolveString("Hello, ${name}!", nil, 10)
	if result != "Hello, world!" {
		t.Errorf("Expected 'Hello, world!', got '%s'", result)
	}
}

// TestVariableResolutionNested tests nested variable references.
func TestVariableResolutionNested(t *testing.T) {
	ve := NewVariableEnvironment(nil)
	ve.SetVariable("inner", "value", "test")
	ve.SetVariable("outer", "${inner}", "test")

	result := ve.ResolveString("${outer}", nil, 10)
	if result != "value" {
		t.Errorf("Expected 'value', got '%s'", result)
	}
}

// TestVariableResolutionChained tests chained variable references.
func TestVariableResolutionChained(t *testing.T) {
	ve := NewVariableEnvironment(nil)
	ve.SetVariable("a", "A", "test")
	ve.SetVariable("b", "${a}B", "test")
	ve.SetVariable("c", "${b}C", "test")

	result := ve.ResolveString("${c}", nil, 10)
	if result != "ABC" {
		t.Errorf("Expected 'ABC', got '%s'", result)
	}
}

// TestVariableResolutionMaxIterations ensures infinite loops are prevented.
func TestVariableResolutionMaxIterations(t *testing.T) {
	ve := NewVariableEnvironment(nil)
	// Create a cycle: a → b → a
	ve.SetVariable("a", "${b}", "test")
	ve.SetVariable("b", "${a}", "test")

	// Should not hang, should return after max iterations
	var errors []string
	result := ve.ResolveString("${a}", &errors, 10)

	// Result will still have unresolved variables due to cycle
	t.Logf("Cyclic resolution result: %s (errors: %v)", result, errors)
	// Just verify it didn't hang - the function completing is the test
}

// TestVariableResolutionUnresolved tests handling of undefined variables.
func TestVariableResolutionUnresolved(t *testing.T) {
	ve := NewVariableEnvironment(nil)
	ve.SetVariable("defined", "value", "test")

	var errors []string
	result := ve.ResolveString("${defined} and ${undefined}", &errors, 10)

	// Defined should resolve, undefined should remain
	if result != "value and ${undefined}" {
		t.Errorf("Expected 'value and ${undefined}', got '%s'", result)
	}

	// Should report the unresolved variable
	if len(errors) == 0 {
		t.Error("Should report unresolved variable 'undefined'")
	}
}

// TestVariableEnvironmentInheritance tests parent-child variable environments.
func TestVariableEnvironmentInheritance(t *testing.T) {
	parent := NewVariableEnvironment(nil)
	parent.SetVariable("from_parent", "parent_value", "parent")
	parent.SetVariable("override_me", "parent_version", "parent")

	child := parent.CreateChild()
	child.SetVariable("from_child", "child_value", "child")
	child.SetVariable("override_me", "child_version", "child")

	// Child should see parent variables
	result := child.ResolveString("${from_parent}", nil, 10)
	if result != "parent_value" {
		t.Errorf("Child should inherit parent variable, got '%s'", result)
	}

	// Child should see its own variables
	result = child.ResolveString("${from_child}", nil, 10)
	if result != "child_value" {
		t.Errorf("Child should see own variable, got '%s'", result)
	}

	// Child should override parent variables
	result = child.ResolveString("${override_me}", nil, 10)
	if result != "child_version" {
		t.Errorf("Child should override parent variable, got '%s'", result)
	}

	// Parent should NOT see child variables
	result = parent.ResolveString("${from_child}", nil, 10)
	if result != "${from_child}" {
		t.Errorf("Parent should not see child variable, got '%s'", result)
	}
}

// TestVariableResolutionPathLike tests path-like variable patterns.
func TestVariableResolutionPathLike(t *testing.T) {
	ve := NewVariableEnvironment(nil)
	ve.SetVariable("base_dir", "/home/user/project", "test")
	ve.SetVariable("build_dir", "${base_dir}/build", "test")
	ve.SetVariable("platform", "linux", "test")
	ve.SetVariable("arch", "x86_64", "test")
	ve.SetVariable("config", "debug", "test")

	// Complex path pattern
	result := ve.ResolveString("${build_dir}/${platform}-${arch}-${config}/bin", nil, 10)
	expected := "/home/user/project/build/linux-x86_64-debug/bin"
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

// TestVariableGetVariable tests direct variable lookup.
func TestVariableGetVariable(t *testing.T) {
	ve := NewVariableEnvironment(nil)
	ve.SetVariable("exists", "value", "test")

	// Should find existing variable
	val, ok := ve.GetVariable("exists")
	if !ok {
		t.Error("Should find 'exists' variable")
	}
	if val != "value" {
		t.Errorf("Expected 'value', got '%s'", val)
	}

	// Should not find missing variable
	_, ok = ve.GetVariable("missing")
	if ok {
		t.Error("Should not find 'missing' variable")
	}
}
