"""
Tests for incremental build functionality
"""

import pytest
import tempfile
import os
import time
from pathlib import Path
from unittest.mock import Mock, MagicMock

from buildy_lib.build_state import BuildState, TaskResult
from buildy_lib.change_detector import ChangeDetector, ChangeSet
from buildy_lib.incremental_builder import IncrementalBuilder
from buildy_lib.cache import BuildCache
from buildy_lib.task_graph import TaskGraph
from buildy_lib.models import BuildTask, TaskInput


class TestBuildState:
    """Tests for BuildState class"""
    
    def test_create_build_state(self):
        """Test creating a BuildState instance"""
        state = BuildState(
            last_build_time=time.time(),
            task_graph_hash="abc123",
            completed_tasks={},
            file_mtimes={"file.cpp": 1234.5},
            config_hash="def456",
            toolchain_hash="ghi789"
        )
        
        assert state.last_build_time > 0
        assert state.task_graph_hash == "abc123"
        assert state.config_hash == "def456"
        assert state.toolchain_hash == "ghi789"
        assert "file.cpp" in state.file_mtimes
    
    def test_save_and_load_build_state(self, tmp_path):
        """Test saving and loading build state"""
        state_file = tmp_path / "build_state.json"
        
        # Create and save state
        original_state = BuildState(
            last_build_time=1234567890.0,
            task_graph_hash="abc123",
            completed_tasks={},
            file_mtimes={"file1.cpp": 1000.0, "file2.cpp": 2000.0},
            config_hash="def456",
            toolchain_hash="ghi789"
        )
        original_state.save(state_file)
        
        # Load state
        loaded_state = BuildState.load(state_file)
        
        assert loaded_state is not None
        assert loaded_state.last_build_time == original_state.last_build_time
        assert loaded_state.task_graph_hash == original_state.task_graph_hash
        assert loaded_state.config_hash == original_state.config_hash
        assert loaded_state.toolchain_hash == original_state.toolchain_hash
        assert loaded_state.file_mtimes == original_state.file_mtimes
    
    def test_load_nonexistent_state(self, tmp_path):
        """Test loading state when file doesn't exist"""
        state_file = tmp_path / "nonexistent.json"
        loaded_state = BuildState.load(state_file)
        assert loaded_state is None
    
    def test_hash_config(self):
        """Test config hashing produces consistent results"""
        config1 = {"key1": "value1", "key2": "value2"}
        config2 = {"key2": "value2", "key1": "value1"}  # Different order
        config3 = {"key1": "value1", "key2": "different"}
        
        hash1 = BuildState.hash_config(config1)
        hash2 = BuildState.hash_config(config2)
        hash3 = BuildState.hash_config(config3)
        
        # Same content, different order should produce same hash
        assert hash1 == hash2
        # Different content should produce different hash
        assert hash1 != hash3
    
    def test_hash_file(self, tmp_path):
        """Test file hashing"""
        test_file = tmp_path / "test.txt"
        test_file.write_text("test content")
        
        hash1 = BuildState.hash_file(str(test_file))
        assert hash1 != ""
        assert len(hash1) == 64  # SHA-256 hex digest length
        
        # Same file should produce same hash
        hash2 = BuildState.hash_file(str(test_file))
        assert hash1 == hash2
        
        # Modified file should produce different hash
        test_file.write_text("different content")
        hash3 = BuildState.hash_file(str(test_file))
        assert hash1 != hash3


class TestChangeSet:
    """Tests for ChangeSet class"""
    
    def test_empty_changeset(self):
        """Test empty changeset has no changes"""
        changes = ChangeSet()
        assert not changes.has_changes()
        assert not changes.requires_full_rebuild()
    
    def test_config_change_requires_full_rebuild(self):
        """Test config change triggers full rebuild"""
        changes = ChangeSet(config_changed=True)
        assert changes.has_changes()
        assert changes.requires_full_rebuild()
    
    def test_toolchain_change_requires_full_rebuild(self):
        """Test toolchain change triggers full rebuild"""
        changes = ChangeSet(toolchain_changed=True)
        assert changes.has_changes()
        assert changes.requires_full_rebuild()
    
    def test_file_changes_dont_require_full_rebuild(self):
        """Test file changes don't trigger full rebuild"""
        changes = ChangeSet(
            modified_files=["file1.cpp"],
            new_files=["file2.cpp"],
            deleted_files=["file3.cpp"]
        )
        assert changes.has_changes()
        assert not changes.requires_full_rebuild()


class TestChangeDetector:
    """Tests for ChangeDetector class"""
    
    @pytest.fixture
    def mock_cache(self):
        """Create a mock cache"""
        cache = Mock(spec=BuildCache)
        cache.cache_index = {}
        return cache
    
    @pytest.fixture
    def build_state(self, tmp_path):
        """Create a test build state"""
        # Create test files
        file1 = tmp_path / "file1.cpp"
        file2 = tmp_path / "file2.cpp"
        file1.write_text("content1")
        file2.write_text("content2")
        
        config = {"key": "value"}
        
        return BuildState(
            last_build_time=time.time(),
            task_graph_hash="abc123",
            completed_tasks={},
            file_mtimes={
                str(file1): os.path.getmtime(file1),
                str(file2): os.path.getmtime(file2)
            },
            config_hash=BuildState.hash_config(config),
            toolchain_hash="toolchain123"
        )
    
    def test_no_changes_detected(self, build_state, mock_cache, tmp_path):
        """Test when nothing has changed"""
        detector = ChangeDetector(build_state, mock_cache)
        
        # Create a dummy toolchain file with same hash
        toolchain_file = tmp_path / "toolchain.yaml"
        toolchain_file.write_text("toolchain content")
        
        # Update build state to have matching hash
        build_state.toolchain_hash = BuildState.hash_file(str(toolchain_file))
        
        config = {"key": "value"}
        changes = detector.detect_changes("config.yaml", config, str(toolchain_file))
        
        # Should detect new files (since we're not tracking all files)
        # but no modifications
        assert not changes.config_changed
        assert not changes.toolchain_changed
        assert len(changes.modified_files) == 0
    
    def test_config_change_detected(self, build_state, mock_cache):
        """Test config change detection"""
        detector = ChangeDetector(build_state, mock_cache)
        
        # Different config
        new_config = {"key": "different_value"}
        changes = detector.detect_changes("config.yaml", new_config, None)
        
        assert changes.config_changed
        assert changes.requires_full_rebuild()
    
    def test_file_modification_detected(self, build_state, mock_cache, tmp_path):
        """Test file modification detection"""
        # Update build state to have empty toolchain hash (no toolchain)
        build_state.toolchain_hash = ""
        
        detector = ChangeDetector(build_state, mock_cache)
        
        # Modify one of the tracked files
        file1 = tmp_path / "file1.cpp"
        time.sleep(0.01)  # Ensure mtime changes
        file1.write_text("modified content")
        
        config = {"key": "value"}
        changes = detector.detect_changes("config.yaml", config, None)
        
        assert str(file1) in changes.modified_files
        assert not changes.config_changed
    
    def test_deleted_file_detected(self, build_state, mock_cache, tmp_path):
        """Test deleted file detection"""
        # Update build state to have empty toolchain hash (no toolchain)
        build_state.toolchain_hash = ""
        
        detector = ChangeDetector(build_state, mock_cache)
        
        # Delete one of the tracked files
        file1 = tmp_path / "file1.cpp"
        file1.unlink()
        
        config = {"key": "value"}
        changes = detector.detect_changes("config.yaml", config, None)
        
        assert str(file1) in changes.deleted_files
    
    def test_get_affected_tasks_direct_dependency(self):
        """Test finding tasks affected by modified files"""
        # Create mock build state and cache
        build_state = BuildState(
            last_build_time=time.time(),
            task_graph_hash="abc",
            completed_tasks={},
            file_mtimes={},
            config_hash="def",
            toolchain_hash=""
        )
        cache = Mock(spec=BuildCache)
        cache.cache_index = {}
        
        detector = ChangeDetector(build_state, cache)
        
        # Create task graph
        graph = TaskGraph()
        task1 = BuildTask(
            task_id="task1",
            task_type="compile",
            command="gcc -c file1.cpp",
            inputs=[TaskInput(path="file1.cpp", hash="hash1")],
            outputs=["file1.o"],
            dependencies=[]
        )
        task2 = BuildTask(
            task_id="task2",
            task_type="link",
            command="gcc file1.o -o app",
            inputs=[TaskInput(path="file1.o", hash="hash2")],
            outputs=["app"],
            dependencies=["task1"]
        )
        graph.add_task(task1)
        graph.add_task(task2)
        
        # Create changes with modified file
        changes = ChangeSet(modified_files=["file1.cpp"])
        
        # Get affected tasks
        affected = detector.get_affected_tasks(changes, graph)
        
        # Both tasks should be affected (task1 directly, task2 transitively)
        assert "task1" in affected
        assert "task2" in affected


class TestIncrementalBuilder:
    """Tests for IncrementalBuilder class"""
    
    @pytest.fixture
    def temp_cache_dir(self, tmp_path):
        """Create a temporary cache directory"""
        cache_dir = tmp_path / ".buildy_cache"
        cache_dir.mkdir()
        return str(cache_dir)
    
    def test_builder_initialization(self, temp_cache_dir):
        """Test IncrementalBuilder initialization"""
        builder = IncrementalBuilder(temp_cache_dir, max_workers=2)
        
        assert builder.cache_dir == Path(temp_cache_dir)
        assert builder.max_workers == 2
        assert builder.build_state is None  # No previous state
    
    def test_first_build_creates_state(self, temp_cache_dir):
        """Test that first build creates build state"""
        builder = IncrementalBuilder(temp_cache_dir)
        state_file = builder.state_file
        
        # Initially no state
        assert not state_file.exists()
        assert builder.build_state is None
    
    def test_load_existing_state(self, temp_cache_dir):
        """Test loading existing build state"""
        # Create a build state
        state_file = Path(temp_cache_dir) / "build_state.json"
        state = BuildState(
            last_build_time=time.time(),
            task_graph_hash="abc123",
            completed_tasks={},
            file_mtimes={"file.cpp": 1234.0},
            config_hash="def456",
            toolchain_hash="ghi789"
        )
        state.save(state_file)
        
        # Create builder - should load state
        builder = IncrementalBuilder(temp_cache_dir)
        
        assert builder.build_state is not None
        assert builder.build_state.task_graph_hash == "abc123"
        assert builder.build_state.config_hash == "def456"
    
    def test_clear_state(self, temp_cache_dir):
        """Test clearing build state"""
        # Create a build state
        state_file = Path(temp_cache_dir) / "build_state.json"
        state = BuildState(
            last_build_time=time.time(),
            task_graph_hash="abc123",
            completed_tasks={},
            file_mtimes={},
            config_hash="def456",
            toolchain_hash=""
        )
        state.save(state_file)
        
        builder = IncrementalBuilder(temp_cache_dir)
        assert builder.build_state is not None
        
        # Clear state
        builder.clear_state()
        
        assert builder.build_state is None
        assert not state_file.exists()


class TestIntegration:
    """Integration tests for incremental builds"""
    
    def test_full_incremental_workflow(self, tmp_path):
        """Test a complete incremental build workflow"""
        # This is a simplified integration test
        # In a real scenario, you'd use actual config files and tasks
        
        cache_dir = tmp_path / ".buildy_cache"
        cache_dir.mkdir()
        
        # First build
        builder1 = IncrementalBuilder(str(cache_dir))
        assert builder1.build_state is None
        
        # Simulate saving state after first build
        state = BuildState(
            last_build_time=time.time(),
            task_graph_hash="graph1",
            completed_tasks={},
            file_mtimes={"file1.cpp": 1000.0},
            config_hash="config1",
            toolchain_hash="toolchain1"
        )
        state.save(builder1.state_file)
        
        # Second build - should load state
        builder2 = IncrementalBuilder(str(cache_dir))
        assert builder2.build_state is not None
        assert builder2.build_state.task_graph_hash == "graph1"
        
        # Verify state persistence
        assert builder2.build_state.file_mtimes["file1.cpp"] == 1000.0


if __name__ == "__main__":
    pytest.main([__file__, "-v"])

