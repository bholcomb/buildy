#!/usr/bin/env python3
"""
Buildy - A prototype task-based build system
Based on the ideal C++ build system design with Blizzard-inspired architecture
"""

import os
import sys
import json
import hashlib
import subprocess
import time
import argparse
import shutil
import shlex
import fcntl
import tempfile
import logging
from pathlib import Path
from dataclasses import dataclass, asdict
from typing import Dict, List, Optional, Set, Any
from concurrent.futures import ThreadPoolExecutor, as_completed
from glob import glob
import yaml

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger('buildy')

# Constants
DEFAULT_TASK_TIMEOUT_SECONDS = 300
DEFAULT_COMPILE_TIME_SECONDS = 2.0
DEFAULT_LINK_TIME_SECONDS = 1.5
DEFAULT_SETUP_TIME_SECONDS = 0.1
DEFAULT_MAX_WORKERS = 4

class VariableEnvironment:
    """Hierarchical variable environment with provenance tracking"""
    
    def __init__(self):
        # Variables stored with their source for provenance
        self.variables: Dict[str, tuple[str, str]] = {}  # name -> (value, source)
        self.scopes: List[str] = []  # Stack of scope names for tracking
    
    def push_scope(self, scope_name: str):
        """Push a new scope onto the stack"""
        self.scopes.append(scope_name)
        logger.debug(f"Entered scope: {scope_name}")
    
    def pop_scope(self):
        """Pop the current scope"""
        if self.scopes:
            scope = self.scopes.pop()
            logger.debug(f"Exited scope: {scope}")
    
    def set_variable(self, name: str, value: str, source: str = None):
        """Set a variable with its source location"""
        if source is None:
            source = self.scopes[-1] if self.scopes else "unknown"
        
        # Override existing variable (higher priority)
        if name in self.variables:
            old_value, old_source = self.variables[name]
            logger.debug(f"Variable '{name}' overridden: '{old_value}' ({old_source}) -> '{value}' ({source})")
        else:
            logger.debug(f"Variable '{name}' set to '{value}' (source: {source})")
        
        self.variables[name] = (value, source)
    
    def get_variable(self, name: str) -> Optional[tuple[str, str]]:
        """Get a variable and its source, returns None if not found"""
        return self.variables.get(name)
    
    def resolve_string(self, text: str, collected_errors: List[str] = None) -> str:
        """Resolve all ${variable} references in a string
        
        Args:
            text: String to resolve
            collected_errors: List to collect unresolved variable errors
            
        Returns:
            Resolved string
        """
        if not isinstance(text, str):
            return text
        
        import re
        
        # Find all ${variable} patterns
        pattern = r'\$\{([^}]+)\}'
        unresolved = []
        
        def replace_var(match):
            var_name = match.group(1)
            result = self.get_variable(var_name)
            if result is None:
                unresolved.append(var_name)
                return match.group(0)  # Keep original if not found
            return result[0]  # Return just the value
        
        resolved = re.sub(pattern, replace_var, text)
        
        # Collect errors if requested
        if unresolved and collected_errors is not None:
            for var in unresolved:
                collected_errors.append(f"Unresolved variable '${{${var}}}' in: {text}")
        
        return resolved
    
    def resolve_recursive(self, value: Any, collected_errors: List[str] = None) -> Any:
        """Recursively resolve variables in nested data structures"""
        if isinstance(value, str):
            return self.resolve_string(value, collected_errors)
        elif isinstance(value, list):
            return [self.resolve_recursive(item, collected_errors) for item in value]
        elif isinstance(value, dict):
            return {k: self.resolve_recursive(v, collected_errors) for k, v in value.items()}
        else:
            return value
    
    def get_all_variables(self) -> Dict[str, Dict[str, str]]:
        """Get all variables with their values and sources for export"""
        return {
            name: {"value": value, "source": source}
            for name, (value, source) in self.variables.items()
        }
    
    def extract_variables_from_section(self, section: Dict[str, Any], source_name: str):
        """Extract variables from a 'variables' section in the config"""
        if not isinstance(section, dict):
            return
        
        variables = section.get('variables', {})
        if not isinstance(variables, dict):
            logger.warning(f"Variables section in '{source_name}' is not a dictionary")
            return
        
        for name, value in variables.items():
            if not isinstance(value, str):
                value = str(value)
            self.set_variable(name, value, source_name)

@dataclass
class ResourceRequirements:
    """Resource requirements for a task"""
    cpu_cores: int = 1
    memory_mb: int = 100
    disk_mb: int = 10

@dataclass
class TaskInput:
    """Input file with content hash"""
    path: str
    hash: str = ""

    def __post_init__(self):
        if not self.hash and os.path.exists(self.path):
            self.hash = self._calculate_hash()

    def _calculate_hash(self) -> str:
        """Calculate SHA-256 hash of file content"""
        try:
            with open(self.path, 'rb') as f:
                return hashlib.sha256(f.read()).hexdigest()
        except (OSError, IOError):
            return "missing_file"

@dataclass
class BuildTask:
    """Individual build task following Blizzard's parse/execute model"""
    task_id: str
    task_type: str
    inputs: List[TaskInput]
    outputs: List[str]
    dependencies: List[str]
    command: str
    platform: str = "linux"
    architecture: str = "x86_64"
    configuration: str = "debug"
    estimated_time: float = 1.0
    resource_requirements: ResourceRequirements = None
    cache_key: str = ""

    def __post_init__(self):
        if self.resource_requirements is None:
            self.resource_requirements = ResourceRequirements()
        if not self.cache_key:
            self.cache_key = self._calculate_cache_key()

    def _calculate_cache_key(self) -> str:
        """Calculate content-addressable cache key"""
        content = {
            'task_type': self.task_type,
            'inputs': [(inp.path, inp.hash) for inp in self.inputs],
            'command': self.command,
            'platform': self.platform,
            'architecture': self.architecture,
            'configuration': self.configuration
        }
        return hashlib.sha256(json.dumps(content, sort_keys=True).encode()).hexdigest()

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

        cache_files_dir = self.cache_dir / task.cache_key
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
            if dep_file and task.task_type == 'compile_cpp':
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

        for cache_key in self.cache_index:
            cache_dir = self.cache_dir / cache_key
            if cache_dir.exists():
                for file_path in cache_dir.rglob('*'):
                    if file_path.is_file():
                        total_size += file_path.stat().st_size

        return {
            'total_entries': total_entries,
            'total_size_mb': total_size / (1024 * 1024),
            'cache_directory': str(self.cache_dir)
        }

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

class ConfigParser:
    """Parse build configuration files with hierarchical variable support"""

    def __init__(self, platform: str = "linux", architecture: str = "x86_64", configuration: str = "debug", 
                 cli_defines: Dict[str, str] = None):
        self.platform = platform
        self.architecture = architecture  
        self.configuration = configuration
        self.global_config = {}
        self.platform_config = {}
        self.arch_config = {}
        self.config_config = {}
        self.config_file_dir = None  # Track the directory of the config file
        self.var_env = VariableEnvironment()  # Variable environment
        self.cli_defines = cli_defines or {}  # CLI-provided variable overrides

    def parse_config_file(self, config_file: str) -> Dict[str, Any]:
        """Parse YAML build configuration file"""
        try:
            # Store the directory of the config file for relative path resolution
            config_path = Path(config_file).resolve()
            self.config_file_dir = config_path.parent
            logger.debug(f"Config file directory: {self.config_file_dir}")
            
            with open(config_file, 'r') as f:
                if config_file.endswith('.json'):
                    config = json.load(f)
                else:
                    config = yaml.safe_load(f) or {}
            
            # Validate configuration
            validation_errors = self._validate_config(config)
            if validation_errors:
                for error in validation_errors:
                    logger.error(f"Config validation error: {error}")
                return {}
            
            return config
        except (FileNotFoundError, yaml.YAMLError, json.JSONDecodeError) as e:
            logger.error(f"Error parsing config file {config_file}: {e}")
            return {}
    
    def _validate_config(self, config: Dict[str, Any]) -> List[str]:
        """Validate configuration and return list of errors"""
        errors = []
        
        # Validate project section
        project = config.get('project', {})
        if not project:
            errors.append("Missing 'project' section")
        elif not isinstance(project, dict):
            errors.append("'project' section must be a dictionary")
        else:
            if 'name' not in project:
                errors.append("Missing 'project.name'")
            elif not isinstance(project['name'], str) or not project['name'].strip():
                errors.append("'project.name' must be a non-empty string")
        
        # Validate libraries
        libraries = config.get('libraries', [])
        if libraries:
            if not isinstance(libraries, list):
                libraries = [libraries]
            for i, lib in enumerate(libraries):
                if not isinstance(lib, dict):
                    errors.append(f"Library {i} must be a dictionary")
                    continue
                if 'name' not in lib:
                    errors.append(f"Library {i} missing 'name'")
                elif not isinstance(lib['name'], str):
                    errors.append(f"Library {i} 'name' must be a string")
                if 'sources' not in lib:
                    errors.append(f"Library {i} missing 'sources'")
        
        # Validate executables
        executables = config.get('executables', [])
        if executables:
            if not isinstance(executables, list):
                executables = [executables]
            for i, exe in enumerate(executables):
                if not isinstance(exe, dict):
                    errors.append(f"Executable {i} must be a dictionary")
                    continue
                if 'name' not in exe:
                    errors.append(f"Executable {i} missing 'name'")
                elif not isinstance(exe['name'], str):
                    errors.append(f"Executable {i} 'name' must be a string")
                if 'sources' not in exe:
                    errors.append(f"Executable {i} missing 'sources'")
        
        # Validate output paths don't escape project
        output_config = config.get('output', {})
        if output_config:
            base_dir = output_config.get('base_dir', 'build')
            if '..' in base_dir or base_dir.startswith('/'):
                errors.append(f"Invalid output base_dir '{base_dir}' - must be relative and not contain '..'")
        
        return errors

    def generate_tasks(self, config: Dict[str, Any]) -> List[BuildTask]:
        """Generate tasks from configuration with variable resolution"""
        tasks = []
        task_counter = 1
        
        # Build variable environment hierarchy
        # Priority (lowest to highest): workspace -> project -> platform -> arch -> config -> built-ins -> CLI
        
        # 1. Workspace-level variables (from top-level 'variables' section)
        self.var_env.push_scope("workspace")
        self.var_env.extract_variables_from_section(config, "workspace")
        
        # Extract configuration sections
        project = config.get('project', {})
        global_config = config.get('config', {})
        platforms = config.get('platforms', {})
        architectures = config.get('architectures', {})
        configurations = config.get('configurations', {})
        
        # 2. Project-level variables
        self.var_env.push_scope("project")
        self.var_env.extract_variables_from_section(project, "project")
        
        # 3. Platform-specific variables
        platform_config = platforms.get(self.platform, {})
        self.var_env.push_scope(f"platform[{self.platform}]")
        self.var_env.extract_variables_from_section(platform_config, f"platform[{self.platform}]")
        
        # 4. Architecture-specific variables
        arch_config = architectures.get(self.architecture, {})
        self.var_env.push_scope(f"architecture[{self.architecture}]")
        self.var_env.extract_variables_from_section(arch_config, f"architecture[{self.architecture}]")
        
        # 5. Configuration-specific variables
        config_config = configurations.get(self.configuration, {})
        self.var_env.push_scope(f"configuration[{self.configuration}]")
        self.var_env.extract_variables_from_section(config_config, f"configuration[{self.configuration}]")
        
        # 6. Built-in variables (highest priority except CLI)
        self.var_env.push_scope("built-in")
        self.var_env.set_variable("platform", self.platform, "built-in")
        self.var_env.set_variable("arch", self.architecture, "built-in")
        self.var_env.set_variable("config", self.configuration, "built-in")
        self.var_env.set_variable("project_name", project.get('name', 'unknown'), "built-in")
        self.var_env.set_variable("project_version", str(project.get('version', '0.0.0')), "built-in")
        
        # Also set base_dir early from output config if it exists
        output_config = config.get('output', {})
        if 'base_dir' in output_config:
            self.var_env.set_variable("base_dir", output_config['base_dir'], "output.base_dir")
        
        # 7. CLI-provided overrides (highest priority)
        if self.cli_defines:
            self.var_env.push_scope("cli")
            for name, value in self.cli_defines.items():
                self.var_env.set_variable(name, value, "cli")
        
        # Now resolve all variables in the config before proceeding
        errors = []
        resolved_config = self.var_env.resolve_recursive(config, errors)
        
        # Check for unresolved variables and fail if any found
        if errors:
            logger.error("Unresolved variables found in configuration:")
            for error in errors:
                logger.error(f"  - {error}")
            raise ValueError(f"Configuration contains {len(errors)} unresolved variable(s)")
        
        # Re-extract sections from resolved config
        project = resolved_config.get('project', {})
        global_config = resolved_config.get('config', {})
        platforms = resolved_config.get('platforms', {})
        architectures = resolved_config.get('architectures', {})
        configurations = resolved_config.get('configurations', {})

        # Merge configuration hierarchy
        merged_config = self._merge_configs(
            global_config, 
            platforms.get(self.platform, {}),
            architectures.get(self.architecture, {}),
            configurations.get(self.configuration, {})
        )

        # Generate setup task
        output_dir = self._get_output_dir(resolved_config)
        setup_task = self._create_setup_task(task_counter, output_dir)
        tasks.append(setup_task)
        task_counter += 1

        # Generate library tasks (use resolved_config so variables in paths are resolved)
        libraries = resolved_config.get('libraries', [])
        if not isinstance(libraries, list):
            libraries = [libraries] if libraries else []

        for lib in libraries:
            lib_tasks = self._generate_library_tasks(lib, merged_config, output_dir, setup_task.task_id, task_counter)
            tasks.extend(lib_tasks)
            task_counter += len(lib_tasks)

        # Generate executable tasks (use resolved_config so variables in paths are resolved)
        executables = resolved_config.get('executables', [])
        if not isinstance(executables, list):
            executables = [executables] if executables else []

        for exe in executables:
            exe_tasks = self._generate_executable_tasks(exe, merged_config, output_dir, setup_task.task_id, task_counter, tasks)
            tasks.extend(exe_tasks)
            task_counter += len(exe_tasks)

        return tasks

    def _merge_configs(self, *configs) -> Dict[str, Any]:
        """Merge configuration hierarchy"""
        merged = {}
        for config in configs:
            if isinstance(config, dict):
                for key, value in config.items():
                    if key in merged and isinstance(merged[key], list) and isinstance(value, list):
                        merged[key] = merged[key] + value
                    else:
                        merged[key] = value
        return merged

    def _get_output_dir(self, config: Dict[str, Any]) -> str:
        """Get output directory pattern and resolve template variables"""
        output_config = config.get('output', {})
        base_dir = output_config.get('base_dir', 'build')
        
        # Note: base_dir is already set in generate_tasks, so don't override it here
        # Just use whatever is in the variable environment
        
        pattern = output_config.get('pattern', f"${{base_dir}}/{self.platform}-{self.architecture}-{self.configuration}")
        
        # Resolve all variables in the pattern
        errors = []
        resolved_pattern = self.var_env.resolve_string(pattern, errors)
        
        if errors:
            logger.error(f"Unresolved variables in output pattern: {errors}")
            raise ValueError(f"Output pattern contains unresolved variables")
        
        logger.debug(f"Output directory pattern '{pattern}' resolved to '{resolved_pattern}'")
        return resolved_pattern

    def _create_setup_task(self, task_id: int, output_dir: str) -> BuildTask:
        """Create directory setup task"""
        return BuildTask(
            task_id=f"setup_dirs_{task_id:03d}",
            task_type="setup",
            inputs=[],
            outputs=[
                f"{output_dir}/lib/",
                f"{output_dir}/bin/", 
                f"{output_dir}/obj/"
            ],
            dependencies=[],
            command=f"mkdir -p {output_dir}/lib {output_dir}/bin {output_dir}/obj",
            platform=self.platform,
            architecture=self.architecture,
            configuration=self.configuration,
            estimated_time=DEFAULT_SETUP_TIME_SECONDS,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=10, disk_mb=1)
        )

    def _generate_library_tasks(self, lib_config: Dict[str, Any], merged_config: Dict[str, Any], 
                               output_dir: str, setup_task_id: str, task_counter: int) -> List[BuildTask]:
        """Generate tasks for a library"""
        tasks = []
        lib_name = lib_config.get('name', f'lib_{task_counter}')
        sources = lib_config.get('sources', [])
        include_dirs = lib_config.get('include_dirs', [])

        if isinstance(sources, str):
            # Simple glob pattern - expand it
            sources = self._expand_glob(sources)
        
        # Process include directories (resolve relative to config file)
        resolved_include_dirs = []
        if isinstance(include_dirs, list):
            for inc_dir in include_dirs:
                if self.config_file_dir:
                    # Make include path relative to config file, then relative to cwd
                    abs_inc_path = self.config_file_dir / inc_dir
                    try:
                        rel_inc_path = abs_inc_path.relative_to(Path.cwd())
                        resolved_include_dirs.append(str(rel_inc_path))
                    except ValueError:
                        resolved_include_dirs.append(str(abs_inc_path))
                else:
                    resolved_include_dirs.append(inc_dir)

        compile_task_objs = []

        # Generate compile tasks for each source file
        for source in sources:
            if source.endswith('.cpp') or source.endswith('.c'):
                compile_task = self._create_compile_task(
                    task_counter, source, lib_name, merged_config, output_dir, setup_task_id,
                    include_dirs=resolved_include_dirs,
                    is_shared_library=True  # Add -fPIC for shared libraries
                )
                tasks.append(compile_task)
                compile_task_objs.append(compile_task)
                task_counter += 1

        # Generate link task
        if compile_task_objs:
            link_task = self._create_library_link_task(
                task_counter, lib_name, compile_task_objs, merged_config, output_dir
            )
            tasks.append(link_task)

        return tasks

    def _generate_executable_tasks(self, exe_config: Dict[str, Any], merged_config: Dict[str, Any],
                                  output_dir: str, setup_task_id: str, task_counter: int, 
                                  existing_tasks: List[BuildTask]) -> List[BuildTask]:
        """Generate tasks for an executable"""
        tasks = []
        exe_name = exe_config.get('name', f'exe_{task_counter}')
        sources = exe_config.get('sources', [])
        dependencies = exe_config.get('depends_on', [])
        include_dirs = exe_config.get('include_dirs', [])

        if isinstance(sources, str):
            sources = self._expand_glob(sources)
        
        # Process include directories (resolve relative to config file)
        resolved_include_dirs = []
        if isinstance(include_dirs, list):
            for inc_dir in include_dirs:
                if self.config_file_dir:
                    # Make include path relative to config file, then relative to cwd
                    abs_inc_path = self.config_file_dir / inc_dir
                    try:
                        rel_inc_path = abs_inc_path.relative_to(Path.cwd())
                        resolved_include_dirs.append(str(rel_inc_path))
                    except ValueError:
                        resolved_include_dirs.append(str(abs_inc_path))
                else:
                    resolved_include_dirs.append(inc_dir)

        compile_task_objs = []

        # Generate compile tasks
        for source in sources:
            if source.endswith('.cpp') or source.endswith('.c'):
                compile_task = self._create_compile_task(
                    task_counter, source, exe_name, merged_config, output_dir, setup_task_id,
                    include_dirs=resolved_include_dirs
                )
                tasks.append(compile_task)
                compile_task_objs.append(compile_task)
                task_counter += 1

        # Find library dependencies
        lib_task_objs = []
        lib_dep_ids = []
        for dep in dependencies:
            if dep.startswith('local(') and dep.endswith(')'):
                lib_name = dep[6:-1]  # Extract name from local("name")
                # Find the library link task
                for task in existing_tasks:
                    if task.task_type == 'link_library' and lib_name in task.task_id:
                        lib_task_objs.append(task)
                        lib_dep_ids.append(task.task_id)
                        break

        # Generate executable link task
        if compile_task_objs:
            link_task = self._create_executable_link_task(
                task_counter, exe_name, compile_task_objs, lib_task_objs, 
                lib_dep_ids, merged_config, output_dir
            )
            tasks.append(link_task)

        return tasks

    def _create_compile_task(self, task_id: int, source_file: str, target_name: str,
                           config: Dict[str, Any], output_dir: str, setup_dep: str,
                           include_dirs: List[str] = None, is_shared_library: bool = False) -> BuildTask:
        """Create a compilation task"""
        obj_file = f"{output_dir}/obj/{Path(source_file).stem}.o"

        # Build compiler command
        cpp_standard = config.get('cpp_standard', 'c++20')
        optimization = config.get('optimization', '-O0')
        defines = config.get('defines', [])
        compiler_flags = config.get('compiler_flags', [])

        if isinstance(defines, list):
            define_flags = ' '.join(f'-D{define}' for define in defines)
        else:
            define_flags = ''

        if isinstance(compiler_flags, list):
            flag_str = ' '.join(compiler_flags)
        else:
            flag_str = str(compiler_flags) if compiler_flags else ''
        
        # Build include directory flags
        include_flags = ''
        if include_dirs:
            include_flags = ' '.join(f'-I{inc_dir}' for inc_dir in include_dirs)
        
        # Add -fPIC for shared libraries
        pic_flag = '-fPIC' if is_shared_library else ''
        
        # Generate dependency file for header tracking
        dep_file = obj_file.replace('.o', '.d')
        dep_flags = f"-MMD -MP -MF {dep_file}"

        command = f"g++ {dep_flags} -std={cpp_standard} {flag_str} {define_flags} {pic_flag} {include_flags} -c {source_file} -o {obj_file}"

        return BuildTask(
            task_id=f"compile_{target_name}_{task_id:03d}",
            task_type="compile_cpp",
            inputs=[TaskInput(path=source_file)],
            outputs=[obj_file, dep_file],  # Include .d file as output
            dependencies=[setup_dep],
            command=command,
            platform=self.platform,
            architecture=self.architecture,
            configuration=self.configuration,
            estimated_time=DEFAULT_COMPILE_TIME_SECONDS,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=200, disk_mb=15)
        )

    def _create_library_link_task(self, task_id: int, lib_name: str, compile_tasks: List[BuildTask],
                                 config: Dict[str, Any], output_dir: str) -> BuildTask:
        """Create library linking task"""
        lib_file = f"{output_dir}/lib/lib{lib_name}.so"

        # Collect actual object files from compile tasks (exclude .d files)
        obj_files = []
        dep_ids = []
        for compile_task in compile_tasks:
            # Only include .o files, not .d dependency files
            obj_files.extend([out for out in compile_task.outputs if out.endswith('.o')])
            dep_ids.append(compile_task.task_id)

        # Build command with specific object files
        obj_files_str = ' '.join(obj_files)
        command = f"g++ -shared -fPIC {obj_files_str} -o {lib_file}"

        return BuildTask(
            task_id=f"link_{lib_name}_{task_id:03d}",
            task_type="link_library",
            inputs=[TaskInput(path=obj) for obj in obj_files],
            outputs=[lib_file],
            dependencies=dep_ids,
            command=command,
            platform=self.platform,
            architecture=self.architecture,
            configuration=self.configuration,
            estimated_time=DEFAULT_LINK_TIME_SECONDS,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=150, disk_mb=25)
        )

    def _create_executable_link_task(self, task_id: int, exe_name: str, 
                                   compile_tasks: List[BuildTask], lib_tasks: List[BuildTask],
                                   lib_dep_ids: List[str], config: Dict[str, Any], 
                                   output_dir: str) -> BuildTask:
        """Create executable linking task"""
        exe_file = f"{output_dir}/bin/{exe_name}"

        # Collect object files from compile tasks (exclude .d files)
        obj_files = []
        dep_ids = []
        for compile_task in compile_tasks:
            # Only include .o files, not .d dependency files
            obj_files.extend([out for out in compile_task.outputs if out.endswith('.o')])
            dep_ids.append(compile_task.task_id)
        
        # Add library dependencies
        dep_ids.extend(lib_dep_ids)
        
        # Collect library files for linking
        lib_files = []
        for lib_task in lib_tasks:
            lib_files.extend(lib_task.outputs)
        
        # Build command with specific files
        obj_files_str = ' '.join(obj_files)
        lib_flags = f"-L{output_dir}/lib" if lib_files else ""
        
        # Extract library names from library files for -l flags
        lib_link_flags = []
        for lib_file in lib_files:
            # Extract lib name from libXXX.so -> -lXXX
            lib_name = Path(lib_file).stem
            if lib_name.startswith('lib'):
                lib_link_flags.append(f"-l{lib_name[3:]}")
        
        lib_link_str = ' '.join(lib_link_flags)
        command = f"g++ {obj_files_str} {lib_flags} {lib_link_str} -o {exe_file}"

        return BuildTask(
            task_id=f"link_exe_{exe_name}_{task_id:03d}",
            task_type="link_executable",
            inputs=[TaskInput(path=obj) for obj in obj_files],
            outputs=[exe_file],
            dependencies=dep_ids,
            command=command,
            platform=self.platform,
            architecture=self.architecture,
            configuration=self.configuration,
            estimated_time=DEFAULT_LINK_TIME_SECONDS,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=120, disk_mb=30)
        )

    def _expand_glob(self, pattern: str) -> List[str]:
        """Expand glob pattern to sorted file list with error handling
        
        Patterns are resolved relative to the config file's directory.
        Returns paths relative to the current working directory.
        """
        try:
            if self.config_file_dir is None:
                logger.warning(f"Config file directory not set, using current directory for pattern '{pattern}'")
                search_pattern = pattern
            else:
                # Make pattern relative to config file directory
                search_pattern = str(self.config_file_dir / pattern)
                logger.debug(f"Expanding pattern '{pattern}' as '{search_pattern}'")
            
            results = sorted(glob(search_pattern, recursive=True))
            
            if not results:
                logger.warning(f"Glob pattern '{pattern}' (resolved to '{search_pattern}') matched no files")
                return []
            
            # Convert absolute paths back to relative paths from current directory
            # This makes the paths work correctly in commands
            cwd = Path.cwd()
            relative_results = []
            for result in results:
                result_path = Path(result)
                try:
                    # Try to make it relative to cwd
                    rel_path = result_path.relative_to(cwd)
                    relative_results.append(str(rel_path))
                except ValueError:
                    # If it's not relative to cwd, use absolute path
                    relative_results.append(str(result_path.resolve()))
            
            logger.debug(f"Pattern '{pattern}' matched {len(relative_results)} files: {relative_results}")
            return relative_results
            
        except Exception as e:
            logger.error(f"Error expanding glob '{pattern}': {e}")
            return []

class TaskExecutor:
    """Execute tasks with caching, parallel execution, and resource-aware scheduling"""

    def __init__(self, cache: BuildCache, max_workers: int = 4, max_memory_mb: int = 8192):
        self.cache = cache
        self.max_workers = max_workers
        self.max_memory_mb = max_memory_mb
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

            # Execute command
            # Note: Using shell=True for compatibility with existing command format
            # TODO: Migrate to command lists for better security
            result = subprocess.run(
                task.command,
                shell=True,
                capture_output=True,
                text=True,
                timeout=DEFAULT_TASK_TIMEOUT_SECONDS,
                cwd=os.getcwd()
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

def main():
    """Main entry point"""
    parser = argparse.ArgumentParser(description='Buildy - Task-based build system prototype')
    parser.add_argument('config_files', nargs='+', help='Build configuration files')
    parser.add_argument('--platform', default='linux', help='Target platform')
    parser.add_argument('--architecture', default='x86_64', help='Target architecture') 
    parser.add_argument('--configuration', default='debug', help='Build configuration')
    parser.add_argument('--cache-dir', default='.buildy_cache', help='Cache directory')
    parser.add_argument('--output', help='Output task graph to file')
    parser.add_argument('--dry-run', action='store_true', help='Generate tasks but don\'t execute')
    parser.add_argument('--workers', type=int, default=DEFAULT_MAX_WORKERS, help='Max parallel workers')
    parser.add_argument('--cache-stats', action='store_true', help='Show cache statistics')
    parser.add_argument('--verbose', '-v', action='store_true', help='Enable verbose logging')
    parser.add_argument('--define', '-D', action='append', dest='defines', metavar='VAR=VALUE',
                       help='Define a variable (can be used multiple times, e.g. -D MY_VAR=value)')

    args = parser.parse_args()

    # Configure logging level
    if args.verbose:
        logging.getLogger('buildy').setLevel(logging.DEBUG)
    
    # Parse CLI defines
    cli_defines = {}
    if args.defines:
        for define in args.defines:
            if '=' not in define:
                logger.error(f"Invalid --define format: '{define}' (expected VAR=VALUE)")
                return 1
            var, value = define.split('=', 1)
            cli_defines[var.strip()] = value.strip()
            logger.debug(f"CLI define: {var.strip()}={value.strip()}")

    try:
        # Initialize components
        cache = BuildCache(args.cache_dir)
        config_parser = ConfigParser(args.platform, args.architecture, args.configuration, cli_defines)
        graph = TaskGraph()

        # Show cache stats if requested
        if args.cache_stats:
            stats = cache.get_cache_stats()
            logger.info(f"Cache Statistics:")
            logger.info(f"  Total entries: {stats['total_entries']}")
            logger.info(f"  Total size: {stats['total_size_mb']:.1f} MB")
            logger.info(f"  Cache directory: {stats['cache_directory']}")
            return 0

        # Parse configuration files and generate tasks
        all_tasks = []
        for config_file in args.config_files:
            if not os.path.exists(config_file):
                logger.error(f"Configuration file not found: {config_file}")
                return 1
            
            logger.info(f"Parsing {config_file}...")
            config = config_parser.parse_config_file(config_file)
            if not config:
                logger.error(f"Failed to parse configuration file: {config_file}")
                return 1
            
            tasks = config_parser.generate_tasks(config)
            all_tasks.extend(tasks)

        if not all_tasks:
            logger.error("No tasks generated from configuration files")
            return 1

        # Build task graph
        for task in all_tasks:
            graph.add_task(task)

        try:
            graph.build_execution_stages()
        except ValueError as e:
            logger.error(f"Error building task graph: {e}")
            return 1

        # Output task graph if requested
        if args.output:
            try:
                output_data = {
                    'metadata': {
                        'platform': args.platform,
                        'architecture': args.architecture,
                        'configuration': args.configuration,
                        'generated_at': time.time(),
                        'total_tasks': len(all_tasks)
                    },
                    'resolved_variables': config_parser.var_env.get_all_variables(),
                    'tasks': [asdict(task) for task in all_tasks],
                    'execution_plan': graph.get_execution_plan()
                }

                with open(args.output, 'w') as f:
                    json.dump(output_data, f, indent=2, default=str)

                logger.info(f"Task graph saved to {args.output}")
            except (OSError, IOError) as e:
                logger.error(f"Failed to write task graph to {args.output}: {e}")
                return 1

        # Execute tasks
        executor = TaskExecutor(cache, args.workers)
        success = executor.execute_task_graph(graph, args.dry_run)

        return 0 if success else 1

    except KeyboardInterrupt:
        logger.warning("\nBuild interrupted by user")
        return 130
    except Exception as e:
        logger.error(f"Unexpected error: {e}", exc_info=True)
        return 1

if __name__ == '__main__':
    sys.exit(main())
