"""Content-addressable build cache with thread-safe operations"""

import os
import json
import time
import hashlib
import shutil
import fcntl
import logging
from pathlib import Path
from typing import Dict, List, Any

from .models import BuildTask

logger = logging.getLogger('buildy.cache')

class BuildCache:
    """Content-addressable build cache with thread-safe operations"""

    def __init__(self, cache_dir: str = ".buildy_cache"):
        self.cache_dir = Path(cache_dir)
        self.cache_dir.mkdir(exist_ok=True)
        self.cache_index_file = self.cache_dir / "cache_index.json"
        self.cache_lock_file = self.cache_dir / "cache.lock"
        self.objects_dir = self.cache_dir / "objects"  # Git-style objects directory
        self.objects_dir.mkdir(exist_ok=True)
        self.cache_index = self._load_cache_index()

    def _get_cache_path(self, cache_key: str) -> Path:
        """Get sharded cache directory path using Git-style sharding
        
        Uses first 2 hex characters as subdirectory to distribute files
        across 256 directories (00-ff) for better filesystem performance.
        
        Example:
            cache_key = '6ec1a1ed4e3693526c4ce9e5b89215f860c6a853eacf1f9b3fe5bfc10c90b580'
            returns: .buildy_cache/objects/6e/c1a1ed4e3693526c4ce9e5b89215f860c6a853eacf1f9b3fe5bfc10c90b580/
        
        This scales to millions of cached tasks without filesystem performance degradation.
        """
        shard = cache_key[:2]  # First 2 hex characters (00-ff)
        return self.objects_dir / shard / cache_key
    
    def _load_cache_index(self) -> Dict[str, Any]:
        """Load cache index from disk with locking"""
        if self.cache_index_file.exists():
            try:
                with open(self.cache_index_file, 'r') as f:
                    # Acquire shared lock for reading
                    fcntl.flock(f.fileno(), fcntl.LOCK_SH)
                    try:
                        data = json.load(f)
                        return data
                    finally:
                        fcntl.flock(f.fileno(), fcntl.LOCK_UN)
            except (json.JSONDecodeError, IOError) as e:
                logger.warning(f"Failed to load cache index: {e}")
        return {}

    def _save_cache_index(self):
        """Save cache index to disk atomically with locking"""
        try:
            # Ensure cache directory exists (may be deleted in parallel builds)
            self.cache_dir.mkdir(parents=True, exist_ok=True)
            
            # Write to temporary file first
            temp_file = self.cache_index_file.with_suffix('.tmp')
            with open(temp_file, 'w') as f:
                # Acquire exclusive lock for writing
                fcntl.flock(f.fileno(), fcntl.LOCK_EX)
                try:
                    json.dump(self.cache_index, f, indent=2)
                    f.flush()
                    os.fsync(f.fileno())
                finally:
                    fcntl.flock(f.fileno(), fcntl.LOCK_UN)
            
            # Atomic rename
            temp_file.replace(self.cache_index_file)
        except (OSError, IOError) as e:
            logger.error(f"Failed to save cache index: {e}")

    def has_cached_result(self, task: BuildTask) -> bool:
        """Check if task result is cached and all dependencies are unchanged"""
        cache_entry = self.cache_index.get(task.cache_key)
        if not cache_entry:
            return False

        # Check if all output files exist and match cached hashes
        for output_path, expected_hash in cache_entry.get('outputs', {}).items():
            if not os.path.exists(output_path):
                logger.debug(f"Cache miss for {task.task_id}: output {output_path} not found")
                return False

            # Directories are always considered valid if they exist
            if expected_hash == "directory":
                continue
            
            actual_hash = self._calculate_file_hash(output_path)
            if actual_hash != expected_hash:
                logger.debug(f"Cache miss for {task.task_id}: output {output_path} hash changed")
                return False
        
        # Check header dependencies (for compile tasks)
        header_hashes = cache_entry.get('header_hashes', {})
        if header_hashes:
            for header_path, expected_hash in header_hashes.items():
                # Check if header still exists
                if not os.path.exists(header_path):
                    logger.debug(f"Cache miss for {task.task_id}: header {header_path} deleted")
                    return False
                
                # Check if header content changed
                actual_hash = self._calculate_file_hash(header_path)
                if actual_hash != expected_hash:
                    logger.info(f"Cache miss for {task.task_id}: header {header_path} modified")
                    return False
            
            logger.debug(f"Cache hit for {task.task_id}: {len(header_hashes)} headers unchanged")

        return True

    def restore_cached_result(self, task: BuildTask) -> bool:
        """Restore cached task outputs"""
        cache_entry = self.cache_index.get(task.cache_key)
        if not cache_entry:
            return False

        # Use sharded path for better filesystem performance
        cache_files_dir = self._get_cache_path(task.cache_key)
        if not cache_files_dir.exists():
            return False

        try:
            # Restore output files from cache (skip directories)
            for output_path in task.outputs:
                # Skip directories - they should be created by the task if needed
                if output_path.endswith('/'):
                    os.makedirs(output_path, exist_ok=True)
                    continue
                    
                cached_file = cache_files_dir / Path(output_path).name
                if cached_file.exists():
                    output_dir = os.path.dirname(output_path)
                    if output_dir:
                        os.makedirs(output_dir, exist_ok=True)
                    shutil.copy2(cached_file, output_path)

            logger.info(f"✓ {task.task_id} - cache hit, restored outputs")
            return True
        except PermissionError as e:
            logger.error(f"✗ {task.task_id} - permission denied: {e}")
            return False
        except OSError as e:
            logger.error(f"✗ {task.task_id} - cache restore failed: {e}")
            return False
        except Exception as e:
            logger.error(f"✗ {task.task_id} - unexpected error during restore: {e}")
            return False

    def cache_task_result(self, task: BuildTask, execution_time: float, success: bool):
        """Cache task result after successful execution"""
        if not success:
            return

        try:
            # Use sharded path for better filesystem performance
            cache_files_dir = self._get_cache_path(task.cache_key)
            cache_files_dir.mkdir(parents=True, exist_ok=True)

            # Cache output files (skip directories)
            output_hashes = {}
            dep_file = None
            for output_path in task.outputs:
                if os.path.exists(output_path):
                    # Skip directories - they can't be cached as files
                    if os.path.isdir(output_path):
                        output_hashes[output_path] = "directory"
                        continue
                    
                    # Track .d file for header dependency parsing
                    if output_path.endswith('.d'):
                        dep_file = output_path
                    
                    cached_file = cache_files_dir / Path(output_path).name
                    shutil.copy2(output_path, cached_file)
                    output_hashes[output_path] = self._calculate_file_hash(output_path)
            
            # Parse header dependencies from .d file if this is a compile task
            header_deps = []
            header_hashes = {}
            if dep_file and task.task_type == 'compile':
                header_deps = self._parse_dependency_file(dep_file)
                # Calculate and store hashes for all header dependencies
                for header_path in header_deps:
                    if os.path.exists(header_path):
                        header_hashes[header_path] = self._calculate_file_hash(header_path)
                    else:
                        logger.debug(f"Header dependency not found: {header_path}")

            # Update cache index
            self.cache_index[task.cache_key] = {
                'task_id': task.task_id,
                'task_type': task.task_type,
                'cached_at': time.time(),
                'execution_time': execution_time,
                'outputs': output_hashes,
                'header_dependencies': header_deps,  # NEW: List of header paths
                'header_hashes': header_hashes,      # NEW: Header path -> hash mapping
                'platform': task.platform,
                'architecture': task.architecture,
                'configuration': task.configuration
            }
            self._save_cache_index()

        except PermissionError as e:
            logger.error(f"✗ {task.task_id} - permission denied during caching: {e}")
        except OSError as e:
            logger.error(f"✗ {task.task_id} - caching failed: {e}")
        except Exception as e:
            logger.error(f"✗ {task.task_id} - unexpected error during caching: {e}")

    def _calculate_file_hash(self, file_path: str) -> str:
        """Calculate SHA-256 hash of file efficiently using chunked reading"""
        hash_obj = hashlib.sha256()
        try:
            with open(file_path, 'rb') as f:
                # Read in 8KB chunks for memory efficiency with large files
                for chunk in iter(lambda: f.read(8192), b''):
                    hash_obj.update(chunk)
            return hash_obj.hexdigest()
        except (OSError, IOError) as e:
            logger.warning(f"Failed to hash file {file_path}: {e}")
            return ""
    
    def _parse_dependency_file(self, dep_file: str) -> List[str]:
        """Parse GCC-generated .d file to extract header dependencies
        
        .d file format example:
        build/obj/math.o: examples/math/libsrc/math.cpp \\
          examples/math/include/mymath.h \\
          /usr/include/c++/11/iostream
        
        Returns list of header file paths (relative or absolute)
        """
        if not os.path.exists(dep_file):
            return []
        
        try:
            with open(dep_file, 'r', encoding='utf-8') as f:
                content = f.read()
            
            # Remove target (everything before and including ':')
            if ':' not in content:
                return []
            
            content = content.split(':', 1)[1]
            
            # Remove line continuations (backslash + newline)
            content = content.replace('\\\n', ' ').replace('\\', '')
            
            # Split on whitespace and filter
            all_deps = content.split()
            
            # Filter to only header files (skip .cpp, .c source files)
            headers = [
                dep.strip() for dep in all_deps 
                if dep.strip() and dep.endswith(('.h', '.hpp', '.hxx', '.hh', '.H'))
            ]
            
            logger.debug(f"Parsed {len(headers)} header dependencies from {dep_file}")
            return headers
            
        except Exception as e:
            logger.warning(f"Failed to parse dependency file {dep_file}: {e}")
            return []

    def get_cache_stats(self) -> Dict[str, Any]:
        """Get cache statistics"""
        total_entries = len(self.cache_index)
        total_size = 0

        # Iterate through sharded cache directories
        for cache_key in self.cache_index:
            cache_dir = self._get_cache_path(cache_key)
            if cache_dir.exists():
                for file_path in cache_dir.rglob('*'):
                    if file_path.is_file():
                        total_size += file_path.stat().st_size

        return {
            'total_entries': total_entries,
            'total_size_mb': total_size / (1024 * 1024),
            'cache_directory': str(self.cache_dir)
        }
