"""Task dependency graph with topological sorting"""

import logging
from typing import Dict, List, Any

from .models import BuildTask

logger = logging.getLogger('buildy.task_graph')

class TaskGraph:
    """Task dependency graph with topological sorting"""

    def __init__(self):
        self.tasks: Dict[str, BuildTask] = {}
        self.execution_stages: List[List[str]] = []

    def add_task(self, task: BuildTask):
        """Add task to graph"""
        self.tasks[task.task_id] = task

    def build_execution_stages(self) -> List[List[str]]:
        """Build parallel execution stages using topological sort"""
        # Build dependency graph
        dependencies = {}
        dependents = {}

        for task_id, task in self.tasks.items():
            dependencies[task_id] = set(task.dependencies)
            dependents[task_id] = set()

        # Build reverse dependencies
        for task_id, deps in dependencies.items():
            for dep in deps:
                if dep in dependents:
                    dependents[dep].add(task_id)

        # Topological sort with parallel stages
        stages = []
        remaining_tasks = set(self.tasks.keys())

        while remaining_tasks:
            # Find tasks with no remaining dependencies
            ready_tasks = []
            for task_id in remaining_tasks:
                if not dependencies[task_id]:
                    ready_tasks.append(task_id)

            if not ready_tasks:
                # Circular dependency detected
                raise ValueError(f"Circular dependency detected in tasks: {remaining_tasks}")

            stages.append(ready_tasks)

            # Remove ready tasks and update dependencies
            for task_id in ready_tasks:
                remaining_tasks.remove(task_id)
                for dependent in dependents[task_id]:
                    dependencies[dependent].discard(task_id)

        self.execution_stages = stages
        return stages

    def get_execution_plan(self) -> Dict[str, Any]:
        """Get detailed execution plan"""
        if not self.execution_stages:
            self.build_execution_stages()

        total_time = 0
        max_parallel = 0
        total_cpu_hours = 0

        for stage in self.execution_stages:
            stage_time = 0
            stage_cpus = 0
            for task_id in stage:
                task = self.tasks[task_id]
                stage_time = max(stage_time, task.estimated_time)
                stage_cpus += task.resource_requirements.cpu_cores
                total_cpu_hours += (task.estimated_time / 3600) * task.resource_requirements.cpu_cores

            total_time += stage_time
            max_parallel = max(max_parallel, len(stage))

        return {
            'total_stages': len(self.execution_stages),
            'total_tasks': len(self.tasks),
            'estimated_time_seconds': total_time,
            'max_parallel_tasks': max_parallel,
            'total_cpu_hours': total_cpu_hours,
            'stages': [
                {f'stage_{i+1}': stage} 
                for i, stage in enumerate(self.execution_stages)
            ]
        }


