"""Tests for BuildCache class"""

import pytest
import time
from pathlib import Path
from buildy_lib.cache import BuildCache
from buildy_lib.models import BuildTask, TaskInput, ResourceRequirements


class TestBuildCache:
    """Test content-addressable build cache"""
    
    def test_cache_initialization(self, temp_dir):
        """Test that cache directory is created"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        assert cache.cache_dir.exists()
        assert cache.objects_dir.exists()
    
    def test_cache_task_result(self, temp_dir, create_source_file):
        """Test caching a task result"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        # Create a fake output file
        output_file = create_source_file('output.o', 'compiled binary content')
        
        task = BuildTask(
            task_id='compile_test_001',
            task_type='compile',
            inputs=[TaskInput(path='test.cpp')],
            outputs=[str(output_file)],
            dependencies=[],
            command='g++ -c test.cpp -o output.o',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        cache.cache_task_result(task, execution_time=1.5, success=True)
        
        # Check that cache entry was created
        assert task.cache_key in cache.cache_index
        assert cache.cache_index[task.cache_key]['task_id'] == 'compile_test_001'
    
    def test_has_cached_result(self, temp_dir, create_source_file):
        """Test checking if a task result is cached"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        output_file = create_source_file('output.o', 'compiled binary content')
        
        task = BuildTask(
            task_id='compile_test_001',
            task_type='compile',
            inputs=[TaskInput(path='test.cpp')],
            outputs=[str(output_file)],
            dependencies=[],
            command='g++ -c test.cpp -o output.o',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        # Initially not cached
        assert not cache.has_cached_result(task)
        
        # Cache it
        cache.cache_task_result(task, execution_time=1.5, success=True)
        
        # Now should be cached
        assert cache.has_cached_result(task)
    
    def test_restore_cached_result(self, temp_dir, create_source_file):
        """Test restoring a cached task result"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        output_file = create_source_file('output.o', 'compiled binary content')
        
        task = BuildTask(
            task_id='compile_test_001',
            task_type='compile',
            inputs=[TaskInput(path='test.cpp')],
            outputs=[str(output_file)],
            dependencies=[],
            command='g++ -c test.cpp -o output.o',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        # Cache the result
        cache.cache_task_result(task, execution_time=1.5, success=True)
        
        # Delete the output file
        output_file.unlink()
        assert not output_file.exists()
        
        # Restore from cache
        success = cache.restore_cached_result(task)
        
        assert success
        assert output_file.exists()
    
    def test_cache_invalidation_on_file_change(self, temp_dir, create_source_file):
        """Test that cache is invalidated when output file changes"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        output_file = create_source_file('output.o', 'original content')
        
        task = BuildTask(
            task_id='compile_test_001',
            task_type='compile',
            inputs=[TaskInput(path='test.cpp')],
            outputs=[str(output_file)],
            dependencies=[],
            command='g++ -c test.cpp -o output.o',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        # Cache the result
        cache.cache_task_result(task, execution_time=1.5, success=True)
        assert cache.has_cached_result(task)
        
        # Modify the output file
        with open(output_file, 'w') as f:
            f.write('modified content')
        
        # Cache should be invalidated
        assert not cache.has_cached_result(task)
    
    def test_header_dependency_tracking(self, temp_dir, create_source_file):
        """Test that header dependencies are tracked"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        # Create source and header files
        source_file = create_source_file('test.cpp', '#include "test.h"\nint main() {}')
        header_file = create_source_file('test.h', '#define VALUE 42')
        output_file = create_source_file('output.o', 'compiled content')
        dep_file = create_source_file('output.d', f'output.o: test.cpp \\\n  {header_file}')
        
        task = BuildTask(
            task_id='compile_test_001',
            task_type='compile',
            inputs=[TaskInput(path=str(source_file))],
            outputs=[str(output_file), str(dep_file)],
            dependencies=[],
            command='g++ -c test.cpp -o output.o',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        # Cache the result
        cache.cache_task_result(task, execution_time=1.5, success=True)
        
        # Check that header dependency was tracked
        cache_entry = cache.cache_index[task.cache_key]
        assert 'header_dependencies' in cache_entry
        assert str(header_file) in cache_entry['header_dependencies']
    
    def test_cache_invalidation_on_header_change(self, temp_dir, create_source_file):
        """Test that cache is invalidated when header file changes"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        source_file = create_source_file('test.cpp', '#include "test.h"\nint main() {}')
        header_file = create_source_file('test.h', '#define VALUE 42')
        output_file = create_source_file('output.o', 'compiled content')
        dep_file = create_source_file('output.d', f'output.o: test.cpp \\\n  {header_file}')
        
        task = BuildTask(
            task_id='compile_test_001',
            task_type='compile',
            inputs=[TaskInput(path=str(source_file))],
            outputs=[str(output_file), str(dep_file)],
            dependencies=[],
            command='g++ -c test.cpp -o output.o',
            platform='linux',
            architecture='x86_64',
            configuration='debug',
            estimated_time=1.0,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
        )
        
        # Cache the result
        cache.cache_task_result(task, execution_time=1.5, success=True)
        assert cache.has_cached_result(task)
        
        # Modify the header file
        time.sleep(0.01)  # Ensure timestamp changes
        with open(header_file, 'w') as f:
            f.write('#define VALUE 100')
        
        # Cache should be invalidated
        assert not cache.has_cached_result(task)
    
    def test_git_style_sharding(self, temp_dir):
        """Test that cache uses Git-style sharding"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        # Create a task with known cache key
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
        
        cache_path = cache._get_cache_path(task.cache_key)
        
        # Check that path uses first 2 characters as shard
        shard = task.cache_key[:2]
        assert str(cache_path).endswith(f'objects/{shard}/{task.cache_key}')
    
    def test_cache_stats(self, temp_dir, create_source_file):
        """Test cache statistics"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        # Cache a few tasks
        for i in range(3):
            output_file = create_source_file(f'output{i}.o', f'content {i}')
            task = BuildTask(
                task_id=f'compile_test_{i:03d}',
                task_type='compile',
                inputs=[TaskInput(path=f'test{i}.cpp')],
                outputs=[str(output_file)],
                dependencies=[],
                command=f'g++ -c test{i}.cpp',
                platform='linux',
                architecture='x86_64',
                configuration='debug',
                estimated_time=1.0,
                resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=10)
            )
            cache.cache_task_result(task, execution_time=1.5, success=True)
        
        stats = cache.get_cache_stats()
        
        assert stats['total_entries'] == 3
        assert stats['total_size_mb'] > 0
        assert 'cache_directory' in stats
    
    def test_failed_task_not_cached(self, temp_dir):
        """Test that failed tasks are not cached"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        task = BuildTask(
            task_id='compile_test_001',
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
        
        # Try to cache a failed task
        cache.cache_task_result(task, execution_time=1.5, success=False)
        
        # Should not be cached
        assert task.cache_key not in cache.cache_index
    
    def test_parse_dependency_file(self, temp_dir, create_source_file):
        """Test parsing GCC-generated .d files"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        dep_content = """output.o: src/main.cpp \\
  include/header1.h \\
  include/header2.hpp \\
  include/inline_impl.inl \\
  /usr/include/c++/11/iostream
"""
        dep_file = create_source_file('output.d', dep_content)
        
        headers = cache._parse_dependency_file(str(dep_file))
        
        assert 'include/header1.h' in headers
        assert 'include/header2.hpp' in headers
        assert 'include/inline_impl.inl' in headers
        assert '/usr/include/c++/11/iostream' not in headers  # System headers excluded
        assert 'src/main.cpp' not in headers  # Source files excluded
    
    def test_parse_msvc_dependency_file(self, temp_dir, create_source_file):
        """Test parsing MSVC /sourceDependencies JSON files"""
        cache = BuildCache(str(temp_dir / 'cache'))
        
        dep_content = """{
  "Version": "1.1",
  "Data": {
    "Source": "C:\\\\project\\\\src\\\\main.cpp",
    "Includes": [
      "C:\\\\project\\\\include\\\\header1.h",
      "C:\\\\project\\\\include\\\\header2.hpp",
      "C:\\\\project\\\\include\\\\inline_impl.inl",
      "C:\\\\Program Files\\\\Microsoft Visual Studio\\\\include\\\\iostream",
      "C:\\\\Windows\\\\System32\\\\winbase.h"
    ]
  }
}"""
        dep_file = create_source_file('output.json', dep_content)
        
        headers = cache._parse_dependency_file(str(dep_file))
        
        # Should include project headers (with normalized paths)
        assert any('header1.h' in h for h in headers)
        assert any('header2.hpp' in h for h in headers)
        assert any('inline_impl.inl' in h for h in headers)
        
        # Should exclude system headers
        assert not any('iostream' in h for h in headers)
        assert not any('winbase.h' in h for h in headers)

