"""
Change detection for incremental builds.
"""

import os
import logging
from dataclasses import dataclass, field
from typing import Dict, Set, Any, List
from pathlib import Path

from .build_state import BuildState
from .cache import BuildCache
from .task_graph import TaskGraph

logger = logging.getLogger(__name__)


@dataclass
class ChangeSet:
    """Set of changes detected since last build"""
    config_changed: bool = False
    toolchain_changed: bool = False
    modified_files: List[str] = field(default_factory=list)
    new_files: List[str] = field(default_factory=list)
    deleted_files: List[str] = field(default_factory=list)
    
    def has_changes(self) -> bool:
        """Check if any changes were detected"""
        return (self.config_changed or 
                self.toolchain_changed or
                bool(self.modified_files) or 
                bool(self.new_files) or 
                bool(self.deleted_files))
    
    def requires_full_rebuild(self) -> bool:
        """Check if changes require a full rebuild"""
        return self.config_changed or self.toolchain_changed


class ChangeDetector:
    """Detect changes since last build"""
    
    def __init__(self, build_state: BuildState, cache: BuildCache):
        self.build_state = build_state
        self.cache = cache
    
    def detect_changes(self, config_file: str, 
                      current_config: Dict[str, Any],
                      toolchain_file: str = None) -> ChangeSet:
        """Detect what changed since last build"""
        changes = ChangeSet()
        
        # Check config hash
        config_hash = BuildState.hash_config(current_config)
        if config_hash != self.build_state.config_hash:
            logger.info("Configuration changed")
            changes.config_changed = True
            return changes  # Full rebuild needed
        
        # Check toolchain hash
        if toolchain_file:
            toolchain_hash = BuildState.hash_file(toolchain_file)
            if toolchain_hash != self.build_state.toolchain_hash:
                logger.info("Toolchain changed")
                changes.toolchain_changed = True
                return changes  # Full rebuild needed
        
        # Check file modifications
        for file_path, old_mtime in self.build_state.file_mtimes.items():
            if not os.path.exists(file_path):
                changes.deleted_files.append(file_path)
                logger.debug(f"Deleted: {file_path}")
                continue
            
            current_mtime = os.path.getmtime(file_path)
            if current_mtime > old_mtime:
                changes.modified_files.append(file_path)
                logger.debug(f"Modified: {file_path}")
        
        # Check for new files (glob patterns in config)
        current_files = self._get_all_source_files(current_config)
        old_files = set(self.build_state.file_mtimes.keys())
        changes.new_files = list(current_files - old_files)
        
        if changes.new_files:
            logger.debug(f"New files: {len(changes.new_files)}")
        
        return changes
    
    def get_affected_tasks(self, changes: ChangeSet, 
                          task_graph: TaskGraph) -> Set[str]:
        """Get task IDs that need to be rebuilt"""
        affected = set()
        
        # Direct dependencies: tasks that use modified files as inputs
        for task_id, task in task_graph.tasks.items():
            for input_file in task.inputs:
                if input_file.path in changes.modified_files:
                    affected.add(task_id)
                    logger.debug(f"Task {task_id} affected by modified input {input_file.path}")
                elif input_file.path in changes.new_files:
                    affected.add(task_id)
                    logger.debug(f"Task {task_id} affected by new input {input_file.path}")
        
        # Header dependencies: check .d files
        for modified_file in changes.modified_files:
            if self._is_header_file(modified_file):
                header_tasks = self._find_tasks_using_header(modified_file)
                affected.update(header_tasks)
                if header_tasks:
                    logger.debug(f"Header {modified_file} affects {len(header_tasks)} tasks")
        
        # Transitive dependencies: tasks that depend on affected tasks
        original_affected = set(affected)
        for task_id in original_affected:
            dependent_tasks = self._get_dependent_tasks(task_id, task_graph)
            affected.update(dependent_tasks)
            if dependent_tasks:
                logger.debug(f"Task {task_id} has {len(dependent_tasks)} dependent tasks")
        
        return affected
    
    def _is_header_file(self, file_path: str) -> bool:
        """Check if file is a header file"""
        header_extensions = {'.h', '.hpp', '.hxx', '.hh', '.inl', '.inc'}
        return any(file_path.endswith(ext) for ext in header_extensions)
    
    def _find_tasks_using_header(self, header_path: str) -> Set[str]:
        """Find all tasks that depend on a header file"""
        affected = set()
        
        # Normalize the header path for comparison
        header_path_normalized = os.path.normpath(header_path)
        
        for cache_key, cache_entry in self.cache.cache_index.items():
            header_deps = cache_entry.get('header_dependencies', [])
            
            # Normalize each dependency path for comparison
            for dep in header_deps:
                dep_normalized = os.path.normpath(dep)
                if dep_normalized == header_path_normalized:
                    task_id = cache_entry.get('task_id')
                    if task_id:
                        affected.add(task_id)
                    break
        
        return affected
    
    def _get_dependent_tasks(self, task_id: str, task_graph: TaskGraph) -> Set[str]:
        """Get all tasks that depend on the given task"""
        dependent = set()
        
        # Get the task's outputs
        if task_id not in task_graph.tasks:
            return dependent
        
        task = task_graph.tasks[task_id]
        # Outputs can be either strings or objects with .path attribute
        task_outputs = set()
        for out in task.outputs:
            if isinstance(out, str):
                task_outputs.add(out)
            else:
                task_outputs.add(out.path)
        
        # Find tasks that use these outputs as inputs
        for other_id, other_task in task_graph.tasks.items():
            if other_id == task_id:
                continue
            
            # Inputs can be either strings or objects with .path attribute
            other_inputs = set()
            for inp in other_task.inputs:
                if isinstance(inp, str):
                    other_inputs.add(inp)
                else:
                    other_inputs.add(inp.path)
            
            if task_outputs & other_inputs:  # Intersection
                dependent.add(other_id)
                # Recursively get dependents
                dependent.update(self._get_dependent_tasks(other_id, task_graph))
        
        return dependent
    
    def _get_all_source_files(self, config: Dict[str, Any]) -> Set[str]:
        """Get all source files from configuration"""
        # This is a simplified version - in practice, you'd need to
        # expand glob patterns and collect all input files
        files = set()
        
        # Traverse config to find file references
        def collect_files(obj):
            if isinstance(obj, dict):
                for key, value in obj.items():
                    if key in ('sources', 'inputs', 'files'):
                        if isinstance(value, list):
                            files.update(str(v) for v in value)
                        elif isinstance(value, str):
                            files.add(value)
                    else:
                        collect_files(value)
            elif isinstance(obj, list):
                for item in obj:
                    collect_files(item)
        
        collect_files(config)
        return files

