"""Execute tasks with caching, parallel execution, and resource-aware scheduling"""

import os
import time
import subprocess
import logging
from typing import Dict, List
from concurrent.futures import ThreadPoolExecutor, as_completed

from .models import BuildTask
from .cache import BuildCache
from .task_graph import TaskGraph
from .execution import ExecutionEnvironment, NativeExecution
from .constants import DEFAULT_TASK_TIMEOUT_SECONDS

logger = logging.getLogger('buildy.executor')

class TaskExecutor:
    """Execute tasks with caching, parallel execution, and resource-aware scheduling"""

    def __init__(self, cache: BuildCache, max_workers: int = 4, max_memory_mb: int = 8192,
                 exec_env: ExecutionEnvironment = None):
        self.cache = cache
        self.max_workers = max_workers
        self.max_memory_mb = max_memory_mb
        self.exec_env = exec_env or NativeExecution()  # Default to native execution
        self.execution_stats = {
            'total_tasks': 0,
            'cache_hits': 0,
            'cache_misses': 0,
            'failed_tasks': 0,
            'total_time': 0
        }

    def execute_task_graph(self, graph: TaskGraph, dry_run: bool = False) -> bool:
        """Execute all tasks in the graph"""
        if not graph.execution_stages:
            graph.build_execution_stages()

        start_time = time.time()
        success = True
        completed_tasks = 0
        total_tasks = len(graph.tasks)

        logger.info(f"Starting build with {total_tasks} tasks in {len(graph.execution_stages)} stages")

        if dry_run:
            self._print_dry_run(graph)
            return True

        for stage_num, stage_tasks in enumerate(graph.execution_stages, 1):
            progress = (completed_tasks / total_tasks) * 100 if total_tasks > 0 else 0
            logger.info(f"\nStage {stage_num}/{len(graph.execution_stages)}: "
                       f"{len(stage_tasks)} task(s) [{progress:.1f}% complete]")

            stage_success = self._execute_stage(stage_tasks, graph.tasks)
            completed_tasks += len(stage_tasks)
            
            if not stage_success:
                success = False
                break

        self.execution_stats['total_time'] = time.time() - start_time
        self._print_summary()

        return success

    def _execute_stage(self, task_ids: List[str], tasks: Dict[str, BuildTask]) -> bool:
        """Execute a single stage of tasks in parallel with resource-aware scheduling"""
        if len(task_ids) == 1:
            # Single task - execute directly
            return self._execute_single_task(tasks[task_ids[0]])

        # Resource-aware parallel execution
        # Calculate total memory requirements for the stage
        total_memory_required = sum(
            tasks[task_id].resource_requirements.memory_mb 
            for task_id in task_ids
        )
        
        # Adjust max workers based on memory constraints
        if total_memory_required > self.max_memory_mb:
            # Need to limit parallelism to avoid memory overload
            effective_workers = max(1, int(self.max_memory_mb / 
                                          max(tasks[tid].resource_requirements.memory_mb 
                                              for tid in task_ids)))
            effective_workers = min(effective_workers, self.max_workers, len(task_ids))
            logger.warning(f"Limiting parallelism to {effective_workers} workers due to memory constraints "
                         f"({total_memory_required}MB required, {self.max_memory_mb}MB available)")
        else:
            effective_workers = min(self.max_workers, len(task_ids))
        
        # Execute tasks in parallel with resource limits
        with ThreadPoolExecutor(max_workers=effective_workers) as executor:
            futures = {
                executor.submit(self._execute_single_task, tasks[task_id]): task_id 
                for task_id in task_ids
            }

            success = True
            for future in as_completed(futures):
                task_success = future.result()
                if not task_success:
                    success = False

            return success

    def _execute_single_task(self, task: BuildTask) -> bool:
        """Execute a single task with caching"""
        self.execution_stats['total_tasks'] += 1

        # Check cache first
        if self.cache.has_cached_result(task):
            if self.cache.restore_cached_result(task):
                self.execution_stats['cache_hits'] += 1
                return True

        # Execute task
        logger.info(f"→ {task.task_id} - executing")
        logger.debug(f"Command: {task.command}")
        start_time = time.time()

        try:
            # Ensure output directories exist
            for output in task.outputs:
                output_dir = os.path.dirname(output)
                if output_dir:
                    os.makedirs(output_dir, exist_ok=True)

            # Execute command using execution environment
            result = self.exec_env.execute(
                command=task.command,
                cwd=os.getcwd(),
                timeout=DEFAULT_TASK_TIMEOUT_SECONDS
            )

            execution_time = time.time() - start_time

            if result.returncode == 0:
                logger.info(f"✓ {task.task_id} ({execution_time:.1f}s) - completed")
                self.cache.cache_task_result(task, execution_time, True)
                self.execution_stats['cache_misses'] += 1
                return True
            else:
                logger.error(f"✗ {task.task_id} - failed: {result.stderr}")
                self.execution_stats['failed_tasks'] += 1
                return False

        except subprocess.TimeoutExpired:
            logger.error(f"✗ {task.task_id} - timeout after {DEFAULT_TASK_TIMEOUT_SECONDS}s")
            self.execution_stats['failed_tasks'] += 1
            return False
        except PermissionError as e:
            logger.error(f"✗ {task.task_id} - permission denied: {e}")
            self.execution_stats['failed_tasks'] += 1
            return False
        except OSError as e:
            logger.error(f"✗ {task.task_id} - OS error: {e}")
            self.execution_stats['failed_tasks'] += 1
            return False
        except Exception as e:
            logger.error(f"✗ {task.task_id} - unexpected error: {e}")
            self.execution_stats['failed_tasks'] += 1
            return False

    def _print_dry_run(self, graph: TaskGraph):
        """Print dry run information"""
        plan = graph.get_execution_plan()

        logger.info(f"Execution Plan:")
        logger.info(f"  Total tasks: {plan['total_tasks']}")
        logger.info(f"  Total stages: {plan['total_stages']}")
        logger.info(f"  Estimated time: {plan['estimated_time_seconds']:.1f}s")
        logger.info(f"  Max parallel tasks: {plan['max_parallel_tasks']}")

        for i, stage_info in enumerate(plan['stages']):
            stage_key = list(stage_info.keys())[0]
            tasks = stage_info[stage_key]
            logger.info(f"  {stage_key}: {tasks}")

    def _print_summary(self):
        """Print execution summary"""
        stats = self.execution_stats
        logger.info(f"\n✓ Build completed in {stats['total_time']:.1f}s")
        logger.info(f"✓ Tasks: {stats['total_tasks']} total, {stats['cache_hits']} cache hits, {stats['cache_misses']} executed")
        if stats['failed_tasks'] > 0:
            logger.error(f"✗ Failed tasks: {stats['failed_tasks']}")


