"""Tests for TaskGraph class"""

import pytest
from buildy_lib.task_graph import TaskGraph
from buildy_lib.models import BuildTask, TaskInput, ResourceRequirements


class TestTaskGraph:
    """Test task dependency graph and topological sorting"""
    
    def test_add_task(self):
        """Test adding tasks to the graph"""
        graph = TaskGraph()
        
        task = BuildTask(
            task_id='test_001',
            task_type='compile',
            inputs=[TaskInput(path='test.cpp')],
            outputs=['output.o'],
            dependencies=[],
            command='g++ -c test.cpp',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        graph.add_task(task)
        
        assert 'test_001' in graph.tasks
        assert graph.tasks['test_001'] == task
    
    def test_simple_linear_execution(self):
        """Test topological sort with linear dependencies"""
        graph = TaskGraph()
        
        task1 = BuildTask(
            task_id='setup',
            task_type='setup',
            inputs=[],
            outputs=['build/'],
            dependencies=[],
            command='mkdir -p build',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=0.1,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=10, disk_mb=1)
        )
        
        task2 = BuildTask(
            task_id='compile',
            task_type='compile',
            inputs=[TaskInput(path='test.cpp')],
            outputs=['output.o'],
            dependencies=['setup'],
            command='g++ -c test.cpp',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        task3 = BuildTask(
            task_id='link',
            task_type='link',
            inputs=[TaskInput(path='output.o')],
            outputs=['program'],
            dependencies=['compile'],
            command='g++ output.o -o program',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=0.5,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=50, disk_mb=20)
        )
        
        graph.add_task(task1)
        graph.add_task(task2)
        graph.add_task(task3)
        
        stages = graph.build_execution_stages()
        
        assert len(stages) == 3
        assert stages[0] == ['setup']
        assert stages[1] == ['compile']
        assert stages[2] == ['link']
    
    def test_parallel_execution(self):
        """Test that independent tasks are in the same stage"""
        graph = TaskGraph()
        
        setup = BuildTask(
            task_id='setup',
            task_type='setup',
            inputs=[],
            outputs=['build/'],
            dependencies=[],
            command='mkdir -p build',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=0.1,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=10, disk_mb=1)
        )
        
        compile1 = BuildTask(
            task_id='compile_1',
            task_type='compile',
            inputs=[TaskInput(path='file1.cpp')],
            outputs=['file1.o'],
            dependencies=['setup'],
            command='g++ -c file1.cpp',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        compile2 = BuildTask(
            task_id='compile_2',
            task_type='compile',
            inputs=[TaskInput(path='file2.cpp')],
            outputs=['file2.o'],
            dependencies=['setup'],
            command='g++ -c file2.cpp',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        graph.add_task(setup)
        graph.add_task(compile1)
        graph.add_task(compile2)
        
        stages = graph.build_execution_stages()
        
        assert len(stages) == 2
        assert stages[0] == ['setup']
        # Both compiles should be in the same stage (parallel)
        assert set(stages[1]) == {'compile_1', 'compile_2'}
    
    def test_diamond_dependency(self):
        """Test diamond-shaped dependency graph"""
        graph = TaskGraph()
        
        # A -> B, C -> D (diamond shape)
        taskA = BuildTask(
            task_id='A',
            task_type='setup',
            inputs=[],
            outputs=['a.out'],
            dependencies=[],
            command='echo A',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=0.1,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=10, disk_mb=1)
        )
        
        taskB = BuildTask(
            task_id='B',
            task_type='compile',
            inputs=[TaskInput(path='a.out')],
            outputs=['b.out'],
            dependencies=['A'],
            command='echo B',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        taskC = BuildTask(
            task_id='C',
            task_type='compile',
            inputs=[TaskInput(path='a.out')],
            outputs=['c.out'],
            dependencies=['A'],
            command='echo C',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        taskD = BuildTask(
            task_id='D',
            task_type='link',
            inputs=[TaskInput(path='b.out'), TaskInput(path='c.out')],
            outputs=['d.out'],
            dependencies=['B', 'C'],
            command='echo D',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=0.5,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=50, disk_mb=20)
        )
        
        graph.add_task(taskA)
        graph.add_task(taskB)
        graph.add_task(taskC)
        graph.add_task(taskD)
        
        stages = graph.build_execution_stages()
        
        assert len(stages) == 3
        assert stages[0] == ['A']
        assert set(stages[1]) == {'B', 'C'}
        assert stages[2] == ['D']
    
    def test_circular_dependency_detection(self):
        """Test that circular dependencies are detected"""
        graph = TaskGraph()
        
        task1 = BuildTask(
            task_id='task1',
            task_type='compile',
            inputs=[TaskInput(path='file1.cpp')],
            outputs=['file1.o'],
            dependencies=['task2'],  # Circular: task1 -> task2 -> task1
            command='g++ -c file1.cpp',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        task2 = BuildTask(
            task_id='task2',
            task_type='compile',
            inputs=[TaskInput(path='file2.cpp')],
            outputs=['file2.o'],
            dependencies=['task1'],  # Circular: task2 -> task1 -> task2
            command='g++ -c file2.cpp',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        graph.add_task(task1)
        graph.add_task(task2)
        
        with pytest.raises(ValueError, match="Circular dependency"):
            graph.build_execution_stages()
    
    def test_get_execution_plan(self):
        """Test getting detailed execution plan"""
        graph = TaskGraph()
        
        setup = BuildTask(
            task_id='setup',
            task_type='setup',
            inputs=[],
            outputs=['build/'],
            dependencies=[],
            command='mkdir -p build',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=0.1,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=10, disk_mb=1)
        )
        
        compile1 = BuildTask(
            task_id='compile_1',
            task_type='compile',
            inputs=[TaskInput(path='file1.cpp')],
            outputs=['file1.o'],
            dependencies=['setup'],
            command='g++ -c file1.cpp',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=2.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        compile2 = BuildTask(
            task_id='compile_2',
            task_type='compile',
            inputs=[TaskInput(path='file2.cpp')],
            outputs=['file2.o'],
            dependencies=['setup'],
            command='g++ -c file2.cpp',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=3.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        graph.add_task(setup)
        graph.add_task(compile1)
        graph.add_task(compile2)
        
        plan = graph.get_execution_plan()
        
        assert plan['total_tasks'] == 3
        assert plan['total_stages'] == 2
        assert plan['max_parallel_tasks'] == 2
        # Estimated time: stage 1 (0.1s) + stage 2 (max of 2.0s and 3.0s = 3.0s) = 3.1s
        assert plan['estimated_time_seconds'] == pytest.approx(3.1, rel=0.1)
    
    def test_empty_graph(self):
        """Test handling of empty graph"""
        graph = TaskGraph()
        
        stages = graph.build_execution_stages()
        
        assert len(stages) == 0
    
    def test_single_task(self):
        """Test graph with single task"""
        graph = TaskGraph()
        
        task = BuildTask(
            task_id='only_task',
            task_type='compile',
            inputs=[TaskInput(path='test.cpp')],
            outputs=['output.o'],
            dependencies=[],
            command='g++ -c test.cpp',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        graph.add_task(task)
        stages = graph.build_execution_stages()
        
        assert len(stages) == 1
        assert stages[0] == ['only_task']

