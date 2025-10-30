"""
Tests for workspace functionality.
"""

import os
import pytest
import tempfile
import shutil
from pathlib import Path

from buildy_lib import (
    Workspace,
    WorkspaceConfig,
    ModuleInfo,
    TargetRegistry,
    TargetReference,
    AmbiguousTargetError,
    TargetNotFoundError,
    VariableEnvironment
)


@pytest.fixture
def temp_workspace():
    """Create a temporary workspace for testing"""
    tmpdir = tempfile.mkdtemp()
    yield Path(tmpdir)
    shutil.rmtree(tmpdir)


def create_buildy_yaml(path: Path, content: dict):
    """Helper to create a buildy.yaml file"""
    import yaml
    with open(path / "buildy.yaml", 'w') as f:
        yaml.dump(content, f)


class TestWorkspaceDiscovery:
    """Test workspace discovery functionality"""
    
    def test_find_workspace_root_current_dir(self, temp_workspace):
        """Test finding workspace root in current directory"""
        create_buildy_yaml(temp_workspace, {"project": {"name": "test"}})
        
        # Change to temp directory and discover
        original_cwd = os.getcwd()
        try:
            os.chdir(temp_workspace)
            root = Workspace._find_workspace_root()
            assert root == temp_workspace
        finally:
            os.chdir(original_cwd)
    
    def test_find_workspace_root_parent_dir(self, temp_workspace):
        """Test finding workspace root in parent directory"""
        create_buildy_yaml(temp_workspace, {"project": {"name": "test"}})
        
        # Create subdirectory
        subdir = temp_workspace / "src" / "lib"
        subdir.mkdir(parents=True)
        
        # Search from subdirectory
        root = Workspace._find_workspace_root(subdir)
        assert root == temp_workspace
    
    def test_find_workspace_root_not_found(self, temp_workspace):
        """Test error when no workspace root found"""
        subdir = temp_workspace / "src"
        subdir.mkdir()
        
        with pytest.raises(FileNotFoundError, match="No buildy.yaml found"):
            Workspace._find_workspace_root(subdir)
    
    def test_is_workspace_root(self, temp_workspace):
        """Test workspace root detection"""
        assert not Workspace.is_workspace_root(temp_workspace)
        
        create_buildy_yaml(temp_workspace, {"project": {"name": "test"}})
        assert Workspace.is_workspace_root(temp_workspace)


class TestModuleDiscovery:
    """Test module discovery functionality"""
    
    def test_discover_single_module(self, temp_workspace):
        """Test discovering a single module"""
        # Create root buildy.yaml
        create_buildy_yaml(temp_workspace, {
            "project": {"name": "root"},
            "tasks": [{"name": "root_task", "type": "executable"}]
        })
        
        workspace = Workspace(temp_workspace)
        modules = workspace.discover_modules()
        
        # Root should be discovered as a module
        assert len(modules) == 1
        assert "." in modules
        assert modules["."].relative_path == "."
        assert "root_task" in modules["."].targets
    
    def test_discover_multiple_modules(self, temp_workspace):
        """Test discovering multiple modules"""
        # Create root
        create_buildy_yaml(temp_workspace, {
            "project": {"name": "root"},
            "workspace": {"discover": ["**/buildy.yaml"]}
        })
        
        # Create module A
        module_a = temp_workspace / "module_a"
        module_a.mkdir()
        create_buildy_yaml(module_a, {
            "project": {"name": "module_a"},
            "tasks": [{"name": "lib_a", "type": "static_library"}]
        })
        
        # Create module B
        module_b = temp_workspace / "module_b"
        module_b.mkdir()
        create_buildy_yaml(module_b, {
            "project": {"name": "module_b"},
            "tasks": [{"name": "lib_b", "type": "shared_library"}]
        })
        
        workspace = Workspace(temp_workspace)
        modules = workspace.discover_modules()
        
        assert len(modules) == 3  # root + 2 modules
        assert "." in modules
        assert "module_a" in modules
        assert "module_b" in modules
        assert "lib_a" in modules["module_a"].targets
        assert "lib_b" in modules["module_b"].targets
    
    def test_discover_nested_modules(self, temp_workspace):
        """Test discovering nested modules"""
        # Create root
        create_buildy_yaml(temp_workspace, {
            "project": {"name": "root"},
            "workspace": {"discover": ["**/buildy.yaml"]}
        })
        
        # Create nested structure: libs/math/buildy.yaml
        nested = temp_workspace / "libs" / "math"
        nested.mkdir(parents=True)
        create_buildy_yaml(nested, {
            "project": {"name": "math"},
            "tasks": [{"name": "math_lib", "type": "static_library"}]
        })
        
        workspace = Workspace(temp_workspace)
        modules = workspace.discover_modules()
        
        assert len(modules) == 2
        assert "libs/math" in modules
        assert "math_lib" in modules["libs/math"].targets
    
    def test_discover_with_exclude_patterns(self, temp_workspace):
        """Test module discovery with exclude patterns"""
        # Create root with exclude pattern
        create_buildy_yaml(temp_workspace, {
            "project": {"name": "root"},
            "workspace": {
                "discover": ["**/buildy.yaml"],
                "exclude": ["tests/**", ".buildy_cache/**"]
            }
        })
        
        # Create module in tests (should be excluded)
        tests_dir = temp_workspace / "tests"
        tests_dir.mkdir()
        create_buildy_yaml(tests_dir, {
            "project": {"name": "tests"},
            "tasks": [{"name": "test_runner", "type": "executable"}]
        })
        
        # Create normal module
        src_dir = temp_workspace / "src"
        src_dir.mkdir()
        create_buildy_yaml(src_dir, {
            "project": {"name": "src"},
            "tasks": [{"name": "app", "type": "executable"}]
        })
        
        workspace = Workspace(temp_workspace)
        modules = workspace.discover_modules()
        
        # tests module should be excluded
        assert "tests" not in modules
        assert "src" in modules


class TestTargetRegistry:
    """Test target registry and dependency resolution"""
    
    def test_target_registry_initialization(self, temp_workspace):
        """Test target registry initialization"""
        # Create workspace with multiple modules
        create_buildy_yaml(temp_workspace, {
            "project": {"name": "root"}
        })
        
        module_a = temp_workspace / "module_a"
        module_a.mkdir()
        create_buildy_yaml(module_a, {
            "project": {"name": "module_a"},
            "tasks": [
                {"name": "lib_a", "type": "static_library"},
                {"name": "test_a", "type": "executable"}
            ]
        })
        
        workspace = Workspace(temp_workspace)
        workspace.discover_modules()
        
        registry = TargetRegistry(workspace)
        registry.initialize()
        
        # Check that targets are registered
        assert "lib_a" in registry._targets_by_name
        assert "test_a" in registry._targets_by_name
    
    def test_resolve_simple_dependency(self, temp_workspace):
        """Test resolving simple target name"""
        create_buildy_yaml(temp_workspace, {"project": {"name": "root"}})
        
        module_a = temp_workspace / "module_a"
        module_a.mkdir()
        create_buildy_yaml(module_a, {
            "project": {"name": "module_a"},
            "tasks": [{"name": "lib_a", "type": "static_library"}]
        })
        
        workspace = Workspace(temp_workspace)
        workspace.discover_modules()
        
        registry = TargetRegistry(workspace)
        registry.initialize()
        
        # Resolve from any context
        ref = registry.resolve_dependency("lib_a", ".")
        assert ref.name == "lib_a"
        assert ref.module_path == "module_a"
        assert ref.full_name == "module_a:lib_a"
    
    def test_resolve_scoped_dependency(self, temp_workspace):
        """Test resolving scoped dependency (module:target)"""
        create_buildy_yaml(temp_workspace, {"project": {"name": "root"}})
        
        module_a = temp_workspace / "module_a"
        module_a.mkdir()
        create_buildy_yaml(module_a, {
            "project": {"name": "module_a"},
            "tasks": [{"name": "lib_a", "type": "static_library"}]
        })
        
        module_b = temp_workspace / "module_b"
        module_b.mkdir()
        create_buildy_yaml(module_b, {
            "project": {"name": "module_b"},
            "tasks": [{"name": "lib_b", "type": "static_library"}]
        })
        
        workspace = Workspace(temp_workspace)
        workspace.discover_modules()
        
        registry = TargetRegistry(workspace)
        registry.initialize()
        
        # Resolve scoped reference
        ref = registry.resolve_dependency("module_a:lib_a", "module_b")
        assert ref.name == "lib_a"
        assert ref.module_path == "module_a"
    
    def test_resolve_local_dependency(self, temp_workspace):
        """Test resolving local dependency (:target)"""
        create_buildy_yaml(temp_workspace, {"project": {"name": "root"}})
        
        module_a = temp_workspace / "module_a"
        module_a.mkdir()
        create_buildy_yaml(module_a, {
            "project": {"name": "module_a"},
            "tasks": [
                {"name": "lib_a", "type": "static_library"},
                {"name": "test_a", "type": "executable"}
            ]
        })
        
        workspace = Workspace(temp_workspace)
        workspace.discover_modules()
        
        registry = TargetRegistry(workspace)
        registry.initialize()
        
        # Resolve local reference from module_a
        ref = registry.resolve_dependency(":lib_a", "module_a")
        assert ref.name == "lib_a"
        assert ref.module_path == "module_a"
    
    def test_ambiguous_target_error(self, temp_workspace):
        """Test error on ambiguous target name"""
        create_buildy_yaml(temp_workspace, {"project": {"name": "root"}})
        
        # Create two modules with same target name
        for module_name in ["module_a", "module_b"]:
            module_dir = temp_workspace / module_name
            module_dir.mkdir()
            create_buildy_yaml(module_dir, {
                "project": {"name": module_name},
                "tasks": [{"name": "lib", "type": "static_library"}]  # Same name!
            })
        
        workspace = Workspace(temp_workspace)
        workspace.discover_modules()
        
        registry = TargetRegistry(workspace)
        registry.initialize()
        
        # Should raise ambiguous error
        with pytest.raises(AmbiguousTargetError) as exc_info:
            registry.resolve_dependency("lib", ".")
        
        # Verify the error message contains both targets
        error_msg = str(exc_info.value)
        assert "module_a:lib" in error_msg
        assert "module_b:lib" in error_msg
    
    def test_target_not_found_error(self, temp_workspace):
        """Test error when target not found"""
        create_buildy_yaml(temp_workspace, {"project": {"name": "root"}})
        
        workspace = Workspace(temp_workspace)
        workspace.discover_modules()
        
        registry = TargetRegistry(workspace)
        registry.initialize()
        
        with pytest.raises(TargetNotFoundError, match="Target 'nonexistent' not found"):
            registry.resolve_dependency("nonexistent", ".")


class TestVariableInheritance:
    """Test variable environment chaining"""
    
    def test_workspace_variables(self, temp_workspace):
        """Test workspace-level variables"""
        create_buildy_yaml(temp_workspace, {
            "project": {"name": "root"},
            "variables": {
                "WORKSPACE_VAR": "workspace_value",
                "SHARED_VAR": "from_workspace"
            }
        })
        
        workspace = Workspace(temp_workspace)
        
        # Check that workspace variables are loaded
        assert "WORKSPACE_VAR" in workspace.config.variables
        assert workspace.config.variables["WORKSPACE_VAR"] == "workspace_value"
    
    def test_module_variable_inheritance(self):
        """Test that module variables inherit from workspace"""
        # Create parent environment (workspace)
        workspace_env = VariableEnvironment()
        workspace_env.push_scope("workspace")
        workspace_env.set_variable("WORKSPACE_VAR", "workspace_value", "workspace")
        workspace_env.set_variable("SHARED_VAR", "from_workspace", "workspace")
        
        # Create child environment (module)
        module_env = VariableEnvironment(parent=workspace_env)
        module_env.push_scope("module")
        module_env.set_variable("MODULE_VAR", "module_value", "module")
        module_env.set_variable("SHARED_VAR", "from_module", "module")  # Override
        
        # Module should see its own variables
        assert module_env.get_variable("MODULE_VAR") == "module_value"
        
        # Module should see workspace variables
        assert module_env.get_variable("WORKSPACE_VAR") == "workspace_value"
        
        # Module override should take precedence
        assert module_env.get_variable("SHARED_VAR") == "from_module"


class TestWorkspaceCaching:
    """Test workspace discovery caching"""
    
    def test_save_and_load_cache(self, temp_workspace):
        """Test saving and loading workspace cache"""
        # Create workspace with modules
        create_buildy_yaml(temp_workspace, {
            "project": {"name": "root"}
        })
        
        module_a = temp_workspace / "module_a"
        module_a.mkdir()
        create_buildy_yaml(module_a, {
            "project": {"name": "module_a"},
            "tasks": [{"name": "lib_a", "type": "static_library"}]
        })
        
        # Discover and save cache
        workspace1 = Workspace(temp_workspace)
        workspace1.discover_modules()
        
        cache_dir = temp_workspace / ".buildy_cache"
        cache_dir.mkdir()
        workspace1.save_discovery_cache(cache_dir)
        
        # Load from cache
        workspace2 = Workspace.load_discovery_cache(cache_dir)
        
        assert workspace2 is not None
        assert workspace2.root_dir == workspace1.root_dir
        assert len(workspace2.modules) == len(workspace1.modules)
        assert "module_a" in workspace2.modules
        assert "lib_a" in workspace2.modules["module_a"].targets
    
    def test_cache_invalidation_missing_root(self, temp_workspace):
        """Test cache invalidation when workspace root is missing"""
        # Create and cache workspace
        create_buildy_yaml(temp_workspace, {"project": {"name": "root"}})
        
        workspace = Workspace(temp_workspace)
        workspace.discover_modules()
        
        cache_dir = temp_workspace / ".buildy_cache"
        cache_dir.mkdir()
        workspace.save_discovery_cache(cache_dir)
        
        # Remove root buildy.yaml
        (temp_workspace / "buildy.yaml").unlink()
        
        # Cache should be invalid
        loaded = Workspace.load_discovery_cache(cache_dir)
        assert loaded is None
    
    def test_cache_invalidation_missing_module(self, temp_workspace):
        """Test cache invalidation when a module is missing"""
        # Create workspace with module
        create_buildy_yaml(temp_workspace, {"project": {"name": "root"}})
        
        module_a = temp_workspace / "module_a"
        module_a.mkdir()
        create_buildy_yaml(module_a, {
            "project": {"name": "module_a"},
            "tasks": [{"name": "lib_a", "type": "static_library"}]
        })
        
        workspace = Workspace(temp_workspace)
        workspace.discover_modules()
        
        cache_dir = temp_workspace / ".buildy_cache"
        cache_dir.mkdir()
        workspace.save_discovery_cache(cache_dir)
        
        # Remove module
        shutil.rmtree(module_a)
        
        # Cache should be invalid
        loaded = Workspace.load_discovery_cache(cache_dir)
        assert loaded is None


class TestWorkspaceStaticMethods:
    """Test workspace static utility methods"""
    
    def test_discover_from_cwd(self, temp_workspace):
        """Test Workspace.discover() static method"""
        create_buildy_yaml(temp_workspace, {"project": {"name": "root"}})
        
        original_cwd = os.getcwd()
        try:
            os.chdir(temp_workspace)
            workspace = Workspace.discover(os.getcwd())
            assert workspace is not None
            assert workspace.root_dir == temp_workspace
        finally:
            os.chdir(original_cwd)
    
    def test_discover_not_found(self, temp_workspace):
        """Test Workspace.discover() returns None when not found"""
        # No buildy.yaml in temp_workspace
        workspace = Workspace.discover(str(temp_workspace))
        assert workspace is None

