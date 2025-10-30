"""
Incremental build management.
"""

import os
import time
import logging
import hashlib
from pathlib import Path
from typing import Dict, Any, Optional

from .build_state import BuildState
from .change_detector import ChangeDetector, ChangeSet
from .cache import BuildCache
from .task_graph import TaskGraph
from .config_parser import ConfigParser
from .executor import TaskExecutor
from .toolchain import ToolchainManager
from .workspace import Workspace

logger = logging.getLogger(__name__)


class IncrementalBuilder:
    """Manage incremental builds"""
    
    def __init__(self, cache_dir: str = ".buildy_cache", max_workers: int = 4):
        self.cache = BuildCache(cache_dir)
        self.cache_dir = Path(cache_dir)
        self.state_file = self.cache_dir / "build_state.json"
        self.build_state = BuildState.load(self.state_file)
        self.max_workers = max_workers
    
    def build(self, config_file: str, 
              config_parser: ConfigParser,
              toolchain_manager: ToolchainManager,
              template_engine,
              variable_env,
              dry_run: bool = False,
              force: bool = False) -> bool:
        """Perform incremental build"""
        # Parse current configuration
        config = config_parser.parse_config_file(config_file)
        
        # Note: Toolchain file lookup happens AFTER task generation
        # because the toolchain is selected during generate_tasks()
        toolchain_file = None
        
        # Force full rebuild if requested
        if force:
            logger.info("Force flag set, performing full rebuild")
            self.build_state = None
        
        # Detect changes
        if self.build_state is None:
            # First build - do full build
            logger.info("No previous build state, performing full build")
            return self._full_build(config_file, config, config_parser, 
                                  toolchain_manager, template_engine, 
                                  variable_env, toolchain_file, dry_run)
        
        detector = ChangeDetector(self.build_state, self.cache)
        changes = detector.detect_changes(config_file, config, toolchain_file)
        
        if changes.requires_full_rebuild():
            logger.info("Configuration or toolchain changed, performing full rebuild")
            return self._full_build(config_file, config, config_parser,
                                  toolchain_manager, template_engine,
                                  variable_env, toolchain_file, dry_run)
        
        if not changes.has_changes():
            logger.info("✓ No changes detected, build is up to date")
            return True
        
        # Incremental build
        change_summary = []
        if changes.modified_files:
            change_summary.append(f"{len(changes.modified_files)} modified")
        if changes.new_files:
            change_summary.append(f"{len(changes.new_files)} new")
        if changes.deleted_files:
            change_summary.append(f"{len(changes.deleted_files)} deleted")
        
        logger.info(f"Incremental build: {', '.join(change_summary)} files")
        return self._incremental_build(config_file, config, config_parser,
                                      toolchain_manager, template_engine,
                                      variable_env, toolchain_file,
                                      changes, dry_run)
    
    def build_workspace(self, workspace: Workspace,
                       config_parser: ConfigParser,
                       toolchain_manager: ToolchainManager,
                       template_engine,
                       target_filter: Optional[list] = None,
                       dry_run: bool = False,
                       force: bool = False) -> bool:
        """
        Perform incremental build for entire workspace.
        
        Args:
            workspace: Workspace to build
            config_parser: ConfigParser (with workspace set)
            toolchain_manager: ToolchainManager instance
            template_engine: BuildTemplateEngine instance
            target_filter: Optional list of target names to build
            dry_run: If True, don't execute commands
            force: If True, force full rebuild
            
        Returns:
            True if build succeeded, False otherwise
        """
        # Force full rebuild if requested
        if force:
            logger.info("Force flag set, performing full rebuild")
            self.build_state = None
        
        # For workspace builds, we track state at workspace level
        workspace_state_file = self.cache_dir / "workspace_state.json"
        workspace_build_state = BuildState.load(workspace_state_file)
        
        # Detect changes at workspace level
        if workspace_build_state is None or force:
            logger.info("No previous workspace state, performing full build")
            return self._full_workspace_build(workspace, config_parser,
                                            toolchain_manager, template_engine,
                                            target_filter, dry_run, workspace_state_file)
        
        # Check if workspace configuration changed
        workspace_config_hash = BuildState.hash_config(workspace.config.raw_config)
        if workspace_config_hash != workspace_build_state.config_hash:
            logger.info("Workspace configuration changed, performing full rebuild")
            return self._full_workspace_build(workspace, config_parser,
                                            toolchain_manager, template_engine,
                                            target_filter, dry_run, workspace_state_file)
        
        # TODO: Implement proper incremental workspace builds
        # For now, just do full build
        logger.info("Workspace incremental builds not yet implemented, performing full build")
        return self._full_workspace_build(workspace, config_parser,
                                        toolchain_manager, template_engine,
                                        target_filter, dry_run, workspace_state_file)
    
    def _full_workspace_build(self, workspace: Workspace,
                             config_parser: ConfigParser,
                             toolchain_manager: ToolchainManager,
                             template_engine,
                             target_filter: Optional[list],
                             dry_run: bool,
                             state_file: Path) -> bool:
        """Perform full workspace build"""
        # Discover modules (with caching)
        workspace.discover_modules()
        
        # Save discovery cache
        workspace.save_discovery_cache(self.cache_dir)
        
        # Generate tasks for all modules
        all_tasks = config_parser.generate_workspace_tasks(target_filter)
        
        # Build task graph
        graph = TaskGraph()
        for task in all_tasks:
            graph.add_task(task)
        
        # Save task graph
        self._save_task_graph(all_tasks, graph, config_parser)
        
        # Execute all tasks
        executor = TaskExecutor(self.cache, max_workers=self.max_workers)
        success = executor.execute_task_graph(graph, dry_run=dry_run)
        
        # Update workspace build state
        if success and not dry_run:
            workspace_config_hash = BuildState.hash_config(workspace.config.raw_config)
            graph_hash = self._hash_graph(graph)
            
            # Collect all file mtimes
            file_mtimes = {}
            for task in all_tasks:
                for inp in task.inputs:
                    path = inp.path if hasattr(inp, 'path') else inp
                    if os.path.exists(path):
                        file_mtimes[path] = os.path.getmtime(path)
            
            # Create new build state
            new_state = BuildState(
                last_build_time=time.time(),
                task_graph_hash=graph_hash,
                completed_tasks=[task.task_id for task in all_tasks],
                file_mtimes=file_mtimes,
                config_hash=workspace_config_hash,
                toolchain_hash=""  # TODO: Track toolchain per module
            )
            new_state.save(state_file)
            logger.info(f"Saved workspace build state to {state_file}")
        
        return success
    
    def _full_build(self, config_file: str,
                   config: Dict[str, Any],
                   config_parser: ConfigParser,
                   toolchain_manager: ToolchainManager,
                   template_engine,
                   variable_env,
                   toolchain_file: Optional[str],
                   dry_run: bool) -> bool:
        """Perform full build"""
        # Generate full task graph
        all_tasks = config_parser.generate_tasks(config)
        
        # Get toolchain file AFTER task generation (when toolchain is known)
        toolchain_file = self._get_toolchain_file(config_parser, toolchain_manager)
        
        graph = TaskGraph()
        for task in all_tasks:
            graph.add_task(task)
        
        # Save task graph
        self._save_task_graph(all_tasks, graph, config_parser)
        
        # Execute all tasks
        executor = TaskExecutor(self.cache, max_workers=self.max_workers)
        success = executor.execute_task_graph(graph, dry_run=dry_run)
        
        # Update build state
        if success and not dry_run:
            self._update_build_state(config, graph, toolchain_file)
        
        return success
    
    def _incremental_build(self, config_file: str,
                          config: Dict[str, Any],
                          config_parser: ConfigParser,
                          toolchain_manager: ToolchainManager,
                          template_engine,
                          variable_env,
                          toolchain_file: Optional[str],
                          changes: ChangeSet,
                          dry_run: bool) -> bool:
        """Perform incremental build of affected tasks"""
        # Generate full task graph (fast, no execution)
        all_tasks = config_parser.generate_tasks(config)
        
        # Get toolchain file AFTER task generation (when toolchain is known)
        toolchain_file = self._get_toolchain_file(config_parser, toolchain_manager)
        
        graph = TaskGraph()
        for task in all_tasks:
            graph.add_task(task)
        
        # Determine which tasks need rebuilding
        detector = ChangeDetector(self.build_state, self.cache)
        affected_task_ids = detector.get_affected_tasks(changes, graph)
        
        if not affected_task_ids:
            logger.info("✓ No tasks affected by changes")
            return True
        
        logger.info(f"Rebuilding {len(affected_task_ids)} affected tasks")
        
        # Invalidate cache for affected tasks
        for task_id in affected_task_ids:
            if task_id in graph.tasks:
                task = graph.tasks[task_id]
                cache_key = task.cache_key
                
                # Remove from cache index
                if cache_key in self.cache.cache_index:
                    logger.debug(f"Invalidating cache for task {task_id}")
                    del self.cache.cache_index[cache_key]
                
                # Remove cached files
                shard = cache_key[:2]
                cache_entry_dir = self.cache.cache_dir / shard / cache_key
                if cache_entry_dir.exists():
                    import shutil
                    shutil.rmtree(cache_entry_dir)
        
        # Save updated cache index
        self.cache._save_cache_index()
        
        # Save task graph
        self._save_task_graph(all_tasks, graph, config_parser)
        
        # Execute only affected tasks
        # We need to create a filtered graph with only affected tasks
        # and their dependencies
        filtered_graph = self._create_filtered_graph(graph, affected_task_ids)
        
        executor = TaskExecutor(self.cache, max_workers=self.max_workers)
        success = executor.execute_task_graph(filtered_graph, dry_run=dry_run)
        
        # Update build state
        if success and not dry_run:
            self._update_build_state(config, graph, toolchain_file)
        
        return success
    
    def _create_filtered_graph(self, full_graph: TaskGraph, 
                              affected_task_ids: set) -> TaskGraph:
        """Create a task graph with only affected tasks"""
        filtered = TaskGraph()
        
        # Add affected tasks and their dependencies
        def add_task_with_deps(task_id: str):
            if task_id in filtered.tasks:
                return  # Already added
            
            if task_id not in full_graph.tasks:
                return  # Task doesn't exist
            
            task = full_graph.tasks[task_id]
            
            # Add dependencies first (from task.dependencies)
            for dep_id in task.dependencies:
                add_task_with_deps(dep_id)
            
            # Add the task
            filtered.add_task(task)
        
        # Add all affected tasks
        for task_id in affected_task_ids:
            add_task_with_deps(task_id)
        
        return filtered
    
    def _update_build_state(self, config: Dict[str, Any], 
                           graph: TaskGraph,
                           toolchain_file: Optional[str]):
        """Update persistent build state"""
        file_mtimes = {}
        
        # Collect all input file modification times
        for task in graph.tasks.values():
            for input_file in task.inputs:
                if os.path.exists(input_file.path):
                    file_mtimes[input_file.path] = os.path.getmtime(input_file.path)
        
        # Hash the task graph
        task_graph_hash = self._hash_graph(graph)
        
        # Hash the config
        config_hash = BuildState.hash_config(config)
        
        # Hash the toolchain file
        toolchain_hash = ""
        if toolchain_file and os.path.exists(toolchain_file):
            toolchain_hash = BuildState.hash_file(toolchain_file)
        
        self.build_state = BuildState(
            last_build_time=time.time(),
            task_graph_hash=task_graph_hash,
            completed_tasks={},  # Could store task results here
            file_mtimes=file_mtimes,
            config_hash=config_hash,
            toolchain_hash=toolchain_hash
        )
        
        self.build_state.save(self.state_file)
        logger.debug("Build state updated")
    
    def _get_toolchain_file(self, config_parser, toolchain_manager) -> Optional[str]:
        """Get the toolchain file path after toolchain selection"""
        if not config_parser.current_toolchain:
            return None
        
        toolchain_name = config_parser.current_toolchain.name
        for tc_file in toolchain_manager.toolchains_dir.glob("*.yaml"):
            # Match by exact stem or if name contains the stem
            if (tc_file.stem == toolchain_name or 
                toolchain_name in tc_file.stem or
                tc_file.stem in toolchain_name):
                logger.debug(f"Found toolchain file: {tc_file}")
                return str(tc_file)
        
        logger.warning(f"Could not find toolchain file for '{toolchain_name}' in {toolchain_manager.toolchains_dir}")
        return None
    
    def _hash_graph(self, graph: TaskGraph) -> str:
        """Generate hash of task graph structure"""
        # Build dependencies dict from tasks
        dependencies = {}
        for task_id, task in graph.tasks.items():
            dependencies[task_id] = sorted(task.dependencies)
        
        # Create a stable representation of the graph
        graph_repr = {
            'tasks': sorted(graph.tasks.keys()),
            'dependencies': {k: v for k, v in sorted(dependencies.items())}
        }
        
        import json
        graph_str = json.dumps(graph_repr, sort_keys=True)
        return hashlib.sha256(graph_str.encode()).hexdigest()
    
    def _save_task_graph(self, all_tasks, graph: TaskGraph, config_parser):
        """Save task graph to tasks.json"""
        try:
            from dataclasses import asdict
            import json
            
            # Get toolchain info
            toolchain_info = {
                'name': config_parser.current_toolchain.name,
                'description': config_parser.current_toolchain.description,
                'target_platform': config_parser.current_toolchain.target_platform,
                'target_architecture': config_parser.current_toolchain.target_architecture,
                'execution_type': config_parser.current_toolchain.execution_type
            }
            
            output_data = {
                'metadata': {
                    'platform': config_parser.platform,
                    'architecture': config_parser.architecture,
                    'configuration': config_parser.configuration,
                    'generated_at': time.time(),
                    'total_tasks': len(all_tasks),
                    'toolchain': toolchain_info
                },
                'resolved_variables': config_parser.var_env.get_all_variables(),
                'tasks': [asdict(task) for task in all_tasks],
                'execution_plan': graph.get_execution_plan()
            }
            
            # Always save to cache directory
            output_path = self.cache_dir / "tasks.json"
            
            with open(output_path, 'w') as f:
                json.dump(output_data, f, indent=2, default=str)
            
            logger.debug(f"Task graph saved to {output_path}")
        except Exception as e:
            logger.warning(f"Failed to write task graph: {e}")
            # Don't fail the build if we can't write the task graph
    
    def clear_state(self):
        """Clear build state to force full rebuild"""
        if self.state_file.exists():
            self.state_file.unlink()
            logger.info("Build state cleared")
        self.build_state = None

