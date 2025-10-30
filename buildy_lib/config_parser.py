"""Parse build configuration files with hierarchical variable support"""

import os
import json
import yaml
import logging
from pathlib import Path
from typing import Dict, List, Any, Optional
from glob import glob

from .models import BuildTask, TaskInput, ResourceRequirements
from .constants import (
    DEFAULT_SETUP_TIME_SECONDS,
    DEFAULT_COMPILE_TIME_SECONDS,
    DEFAULT_LINK_TIME_SECONDS
)
from .variables import VariableEnvironment
from .toolchain import ToolchainManager, ToolchainConfig, ToolMatcher, CommandBuilder
from .execution import ExecutionEnvironment
from .template_engine import BuildTemplateEngine
from .workspace import Workspace, ModuleInfo
from .target_registry import TargetRegistry

logger = logging.getLogger('buildy.config_parser')

class ConfigParser:
    """Parse build configuration files with hierarchical variable support"""

    def __init__(self, platform: str = "linux", architecture: str = "x86_64", configuration: str = "debug", 
                 cli_defines: Dict[str, str] = None, toolchain_manager: ToolchainManager = None,
                 default_toolchain: str = None, template_engine: BuildTemplateEngine = None,
                 workspace: Optional[Workspace] = None, parent_var_env: Optional[VariableEnvironment] = None):
        self.platform = platform
        self.architecture = architecture  
        self.configuration = configuration
        self.global_config = {}
        self.platform_config = {}
        self.arch_config = {}
        self.config_config = {}
        self.config_file_dir = None  # Track the directory of the config file
        self.var_env = VariableEnvironment(parent=parent_var_env)  # Variable environment with optional parent
        self.cli_defines = cli_defines or {}  # CLI-provided variable overrides
        self.toolchain_manager = toolchain_manager  # Toolchain manager
        self.default_toolchain = default_toolchain  # Default toolchain name
        self.template_engine = template_engine or BuildTemplateEngine()  # Build template engine
        self.current_toolchain: Optional[ToolchainConfig] = None  # Current active toolchain
        self.tool_matcher: Optional[ToolMatcher] = None  # Tool matcher for current toolchain
        self.command_builder: Optional[CommandBuilder] = None  # Command builder for current toolchain
        self.exec_env: Optional[ExecutionEnvironment] = None  # Execution environment
        self.workspace = workspace  # Workspace for multi-module support
        self.target_registry: Optional[TargetRegistry] = None  # Target registry for dependency resolution
        self.current_module: Optional[str] = None  # Current module being parsed

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

    def _select_toolchain(self, config: Dict[str, Any]) -> ToolchainConfig:
        """Select toolchain based on hierarchical configuration
        
        Priority (highest to lowest):
        1. CLI --toolchain argument (self.default_toolchain)
        2. Project-level toolchain field
        3. Workspace-level toolchain field
        4. Auto-detect based on platform/architecture
        """
        if not self.toolchain_manager:
            raise ValueError("ToolchainManager not initialized")
        
        # Check CLI override first
        if self.default_toolchain:
            tc = self.toolchain_manager.get_toolchain(self.default_toolchain)
            if tc:
                logger.info(f"Using CLI-specified toolchain: {tc.name}")
                return tc
            else:
                logger.warning(f"CLI toolchain '{self.default_toolchain}' not found, falling back")
        
        # Check project-level toolchain
        project = config.get('project', {})
        project_toolchain = project.get('toolchain')
        if project_toolchain:
            tc = self.toolchain_manager.get_toolchain(project_toolchain)
            if tc:
                logger.info(f"Using project-specified toolchain: {tc.name}")
                return tc
            else:
                logger.warning(f"Project toolchain '{project_toolchain}' not found, falling back")
        
        # Check workspace-level toolchain
        workspace_toolchain = config.get('toolchain')
        if workspace_toolchain:
            tc = self.toolchain_manager.get_toolchain(workspace_toolchain)
            if tc:
                logger.info(f"Using workspace-specified toolchain: {tc.name}")
                return tc
            else:
                logger.warning(f"Workspace toolchain '{workspace_toolchain}' not found, falling back")
        
        # Auto-detect based on platform/architecture
        tc = self.toolchain_manager.auto_detect(self.platform, self.architecture)
        if tc:
            return tc
        
        # No toolchain found
        raise ValueError(f"No suitable toolchain found for {self.platform}-{self.architecture}. "
                        f"Available toolchains: {[name for name, _ in self.toolchain_manager.list_toolchains()]}")
    
    def generate_workspace_tasks(self, target_filter: Optional[List[str]] = None) -> List[BuildTask]:
        """
        Generate tasks for entire workspace or specific targets.
        
        Args:
            target_filter: List of target names to build (None = all targets)
            
        Returns:
            List of BuildTask objects for all modules
        """
        if not self.workspace:
            raise ValueError("No workspace configured. Use generate_tasks() for single-file builds.")
        
        # Initialize target registry
        self.target_registry = TargetRegistry(self.workspace)
        self.target_registry.initialize()
        
        all_tasks = []
        
        # Create workspace-level variable environment
        workspace_var_env = VariableEnvironment()
        workspace_var_env.push_scope("workspace")
        workspace_var_env.extract_variables_from_section(
            self.workspace.config.raw_config.get('variables', {}),
            "workspace"
        )
        
        # Process each module
        for module_path, module_info in self.workspace.modules.items():
            # Skip if target filter specified and this module has no matching targets
            if target_filter:
                has_matching_target = any(
                    target in target_filter for target in module_info.targets
                )
                if not has_matching_target:
                    continue
            
            logger.info(f"Processing module: {module_path}")
            
            # Create module-specific parser with chained variable environment
            module_parser = ConfigParser(
                platform=self.platform,
                architecture=self.architecture,
                configuration=self.configuration,
                cli_defines=self.cli_defines,
                toolchain_manager=self.toolchain_manager,
                default_toolchain=self.default_toolchain,
                template_engine=self.template_engine,
                workspace=self.workspace,
                parent_var_env=workspace_var_env  # Chain to workspace environment
            )
            
            # Set current module for dependency resolution
            module_parser.current_module = module_path
            module_parser.target_registry = self.target_registry
            
            # Set config file directory for base_dir resolution
            module_parser.config_file_dir = module_info.path.parent
            
            # Generate tasks for this module
            module_tasks = module_parser.generate_tasks(module_info.config)
            
            # Resolve cross-module dependencies
            module_tasks = self._resolve_module_dependencies(module_tasks, module_path)
            
            all_tasks.extend(module_tasks)
        
        logger.info(f"Generated {len(all_tasks)} tasks from {len(self.workspace.modules)} modules")
        return all_tasks
    
    def _resolve_module_dependencies(self, tasks: List[BuildTask], current_module: str) -> List[BuildTask]:
        """
        Resolve dependency strings to actual task IDs.
        
        Args:
            tasks: List of tasks from current module
            current_module: Module path for context
            
        Returns:
            Tasks with resolved dependencies
        """
        if not self.target_registry:
            return tasks
        
        for task in tasks:
            resolved_deps = []
            for dep in task.dependencies:
                # If dependency is already a task ID (internal), keep it
                if any(t.task_id == dep for t in tasks):
                    resolved_deps.append(dep)
                    continue
                
                # Try to resolve as target reference
                try:
                    target_ref = self.target_registry.resolve_dependency(dep, current_module)
                    # Convert target reference to task ID
                    # For now, use target name as task ID (will need refinement)
                    resolved_deps.append(target_ref.name)
                    logger.debug(f"Resolved dependency '{dep}' to '{target_ref.full_name}'")
                except Exception as e:
                    logger.warning(f"Failed to resolve dependency '{dep}': {e}")
                    resolved_deps.append(dep)  # Keep original
            
            task.dependencies = resolved_deps
        
        return tasks

    def generate_tasks(self, config: Dict[str, Any]) -> List[BuildTask]:
        """Generate tasks from configuration with variable resolution"""
        tasks = []
        task_counter = 1
        
        # Select and initialize toolchain
        self.current_toolchain = self._select_toolchain(config)
        self.tool_matcher = ToolMatcher(self.current_toolchain)
        self.command_builder = CommandBuilder(self.current_toolchain, self.tool_matcher, self.configuration)
        self.exec_env = ExecutionEnvironment.create(self.current_toolchain)
        
        logger.info(f"Using toolchain: {self.current_toolchain.name} ({self.current_toolchain.description})")
        logger.debug(f"Execution environment: {self.current_toolchain.execution_type}")
        
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
        
        # Also set base_dir early from output config (defaults to "build")
        output_config = config.get('output', {})
        base_dir = output_config.get('base_dir', 'build')
        self.var_env.set_variable("base_dir", base_dir, "output.base_dir")
        
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
        
        # Generate shader tasks (use resolved_config so variables in paths are resolved)
        shaders = resolved_config.get('shaders', [])
        if not isinstance(shaders, list):
            shaders = [shaders] if shaders else []
        
        for shader_group in shaders:
            shader_tasks = self._generate_shader_tasks(shader_group, merged_config, output_dir, setup_task.task_id, task_counter)
            tasks.extend(shader_tasks)
            task_counter += len(shader_tasks)
        
        # Generate texture tasks (use resolved_config so variables in paths are resolved)
        textures = resolved_config.get('textures', [])
        if not isinstance(textures, list):
            textures = [textures] if textures else []
        
        for texture_group in textures:
            texture_tasks = self._generate_texture_tasks(texture_group, merged_config, output_dir, setup_task.task_id, task_counter)
            tasks.extend(texture_tasks)
            task_counter += len(texture_tasks)

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
        """Generate tasks for a library using template engine"""
        # Determine library type (default to shared_library)
        lib_type = lib_config.get('type', 'shared_library')
        
        # Expand glob patterns in sources
        sources = lib_config.get('sources', [])
        if isinstance(sources, str):
            sources = self._expand_glob(sources)
        lib_config['sources'] = sources
        
        # Process include directories (resolve relative to config file)
        include_dirs = lib_config.get('include_dirs', [])
        resolved_include_dirs = []
        if isinstance(include_dirs, list):
            for inc_dir in include_dirs:
                if self.config_file_dir:
                    abs_inc_path = self.config_file_dir / inc_dir
                    try:
                        rel_inc_path = abs_inc_path.relative_to(Path.cwd())
                        resolved_include_dirs.append(str(rel_inc_path))
                    except ValueError:
                        resolved_include_dirs.append(str(abs_inc_path))
                else:
                    resolved_include_dirs.append(inc_dir)
        lib_config['include_dirs'] = resolved_include_dirs
        
        # Use template engine to generate tasks
        tasks = self.template_engine.expand_template(
            template_name=lib_type,
            item_config=lib_config,
            merged_config=merged_config,
            output_dir=output_dir,
            setup_task_id=setup_task_id,
            task_counter=task_counter,
            tool_matcher=self.tool_matcher,
            command_builder=self.command_builder,
            platform=self.platform,
            architecture=self.architecture,
            configuration=self.configuration
        )
        
        return tasks

    def _generate_executable_tasks(self, exe_config: Dict[str, Any], merged_config: Dict[str, Any],
                                  output_dir: str, setup_task_id: str, task_counter: int, 
                                  existing_tasks: List[BuildTask]) -> List[BuildTask]:
        """Generate tasks for an executable using template engine"""
        # Determine executable type (default to executable)
        exe_type = exe_config.get('type', 'executable')
        
        # Expand glob patterns in sources
        sources = exe_config.get('sources', [])
        if isinstance(sources, str):
            sources = self._expand_glob(sources)
        exe_config['sources'] = sources
        
        # Process include directories (resolve relative to config file)
        include_dirs = exe_config.get('include_dirs', [])
        resolved_include_dirs = []
        if isinstance(include_dirs, list):
            for inc_dir in include_dirs:
                if self.config_file_dir:
                    abs_inc_path = self.config_file_dir / inc_dir
                    try:
                        rel_inc_path = abs_inc_path.relative_to(Path.cwd())
                        resolved_include_dirs.append(str(rel_inc_path))
                    except ValueError:
                        resolved_include_dirs.append(str(abs_inc_path))
                else:
                    resolved_include_dirs.append(inc_dir)
        exe_config['include_dirs'] = resolved_include_dirs
        
        # Use template engine to generate tasks
        tasks = self.template_engine.expand_template(
            template_name=exe_type,
            item_config=exe_config,
            merged_config=merged_config,
            output_dir=output_dir,
            setup_task_id=setup_task_id,
            task_counter=task_counter,
            tool_matcher=self.tool_matcher,
            command_builder=self.command_builder,
            platform=self.platform,
            architecture=self.architecture,
            configuration=self.configuration,
            existing_tasks=existing_tasks
        )
        
        return tasks

    def _create_compile_task(self, task_id: int, source_file: str, target_name: str,
                           config: Dict[str, Any], output_dir: str, setup_dep: str,
                           include_dirs: List[str] = None, is_shared_library: bool = False) -> BuildTask:
        """Create a compilation task using tool matching"""
        include_dirs = include_dirs or []
        
        # Find appropriate compile tool for this source file
        tool = self.tool_matcher.find_tool('compile', source_file)
        if not tool:
            raise ValueError(f"No compile tool found for file: {source_file}")
        
        # Build output file path
        obj_file = f"{output_dir}/obj/{Path(source_file).stem}{tool.output_extension}"

        # Extract configuration
        cpp_standard = config.get('cpp_standard', 'c++20')
        defines = config.get('defines', [])
        compiler_flags = config.get('compiler_flags', [])
        
        # Normalize defines to list
        if not isinstance(defines, list):
            defines = [defines] if defines else []
        
        # Normalize compiler flags to list
        if isinstance(compiler_flags, str):
            compiler_flags = [compiler_flags]
        elif not isinstance(compiler_flags, list):
            compiler_flags = []
        
        # Build compile command using tool
        command, dep_file = self.command_builder.build_command(
            tool=tool,
            source=source_file,
            output=obj_file,
            defines=defines,
            include_dirs=include_dirs,
            extra_flags=compiler_flags,
            is_shared_library=is_shared_library,
            std=cpp_standard
        )
        
        # Build outputs list
        outputs = [obj_file]
        if dep_file:
            outputs.append(dep_file)

        return BuildTask(
            task_id=f"compile_{target_name}_{task_id:03d}",
            task_type="compile",
            inputs=[TaskInput(path=source_file)],
            outputs=outputs,
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
        """Create library linking task using tool matching"""
        # Find link tool for shared libraries
        link_tool = self.tool_matcher.find_link_tool('shared_library')
        if not link_tool:
            raise ValueError(f"No link tool found for shared_library")
        
        # Get library filename from tool
        lib_filename = link_tool.output_pattern.format(name=lib_name)
        lib_file = f"{output_dir}/lib/{lib_filename}"

        # Collect actual object files from compile tasks (exclude .d files)
        obj_files = []
        dep_ids = []
        for compile_task in compile_tasks:
            # Only include files with object extension
            for output in compile_task.outputs:
                if any(output.endswith(ext) for ext in link_tool.input_extensions):
                    obj_files.append(output)
            dep_ids.append(compile_task.task_id)

        # Build link command using tool
        command = self.command_builder.build_link_command(
            tool=link_tool,
            objects=obj_files,
            output=lib_file
        )

        return BuildTask(
            task_id=f"link_{lib_name}_{task_id:03d}",
            task_type="link",
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
        """Create executable linking task using tool matching"""
        # Find link tool for executables
        link_tool = self.tool_matcher.find_link_tool('executable')
        if not link_tool:
            raise ValueError(f"No link tool found for executable")
        
        # Get executable filename from tool
        exe_filename = link_tool.output_pattern.format(name=exe_name)
        exe_file = f"{output_dir}/bin/{exe_filename}"

        # Collect object files from compile tasks
        obj_files = []
        dep_ids = []
        for compile_task in compile_tasks:
            # Only include files with object extension
            for output in compile_task.outputs:
                if any(output.endswith(ext) for ext in link_tool.input_extensions):
                    obj_files.append(output)
            dep_ids.append(compile_task.task_id)
        
        # Add library dependencies
        dep_ids.extend(lib_dep_ids)
        
        # Collect library files and extract library names
        lib_dirs = []
        lib_names = []
        
        if lib_tasks:
            lib_dirs.append(f"{output_dir}/lib")
            
            for lib_task in lib_tasks:
                for lib_file in lib_task.outputs:
                    # Extract library name from filename
                    lib_filename = Path(lib_file).stem
                    
                    # Handle different naming conventions
                    # Unix: libXXX.so -> XXX
                    # Windows: XXX.dll or XXX.lib -> XXX
                    if lib_filename.startswith('lib'):
                        lib_names.append(lib_filename[3:])
                    else:
                        lib_names.append(lib_filename)
        
        # Build link command using tool
        command = self.command_builder.build_link_command(
            tool=link_tool,
            objects=obj_files,
            output=exe_file,
            lib_dirs=lib_dirs,
            libs=lib_names
        )

        return BuildTask(
            task_id=f"link_exe_{exe_name}_{task_id:03d}",
            task_type="link",
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
    
    def _generate_shader_tasks(self, shader_config: Dict[str, Any], merged_config: Dict[str, Any],
                               output_dir: str, setup_task_id: str, task_counter: int) -> List[BuildTask]:
        """Generate shader compilation tasks using tool matching
        
        Example shader_config:
        {
            'name': 'game_shaders',
            'toolchain': 'glslc',  # Optional: override toolchain for shaders
            'sources': 'shaders/*.vert',
            'output_dir': 'shaders/spirv',
            'flags': ['-O']
        }
        """
        tasks = []
        shader_name = shader_config.get('name', f'shaders_{task_counter}')
        sources = shader_config.get('sources', [])
        shader_output_dir = shader_config.get('output_dir', f'{output_dir}/shaders')
        custom_flags = shader_config.get('flags', [])
        
        # Check if a custom toolchain is specified for this shader group
        shader_toolchain_name = shader_config.get('toolchain')
        if shader_toolchain_name:
            # Temporarily switch toolchain for shader compilation
            shader_toolchain = self.toolchain_manager.get_toolchain(shader_toolchain_name)
            if not shader_toolchain:
                logger.warning(f"Shader toolchain '{shader_toolchain_name}' not found, using current toolchain")
                shader_toolchain = self.current_toolchain
            shader_tool_matcher = ToolMatcher(shader_toolchain)
            shader_cmd_builder = CommandBuilder(shader_toolchain, shader_tool_matcher, self.configuration)
        else:
            shader_tool_matcher = self.tool_matcher
            shader_cmd_builder = self.command_builder
        
        # Expand source patterns
        if isinstance(sources, str):
            sources = self._expand_glob(sources)
        
        # Generate compilation task for each shader
        for source in sources:
            # Find appropriate tool for this shader file
            tool = shader_tool_matcher.find_tool('compile', source)
            if not tool:
                logger.warning(f"No compile tool found for shader: {source}, skipping")
                continue
            
            output_file = f"{shader_output_dir}/{Path(source).stem}{tool.output_extension}"
            
            # Build shader compile command
            command, _ = shader_cmd_builder.build_command(
                tool=tool,
                source=source,
                output=output_file,
                extra_flags=custom_flags
            )
            
            task = BuildTask(
                task_id=f"shader_{shader_name}_{task_counter:03d}",
                task_type="compile",
                inputs=[TaskInput(path=source)],
                outputs=[output_file],
                dependencies=[setup_task_id],
                command=command,
                platform=self.platform,
                architecture=self.architecture,
                configuration=self.configuration,
                estimated_time=1.0,
                resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=100, disk_mb=5)
            )
            tasks.append(task)
            task_counter += 1
        
        return tasks
    
    def _generate_texture_tasks(self, texture_config: Dict[str, Any], merged_config: Dict[str, Any],
                                output_dir: str, setup_task_id: str, task_counter: int) -> List[BuildTask]:
        """Generate texture conversion tasks using tool matching
        
        Example texture_config:
        {
            'name': 'game_textures',
            'toolchain': 'compressonator',  # or 'texconv', 'imagemagick'
            'sources': 'textures/*.png',
            'output_dir': 'textures/dds',
            'flags': ['-fd', 'BC7']
        }
        """
        tasks = []
        texture_name = texture_config.get('name', f'textures_{task_counter}')
        sources = texture_config.get('sources', [])
        texture_output_dir = texture_config.get('output_dir', f'{output_dir}/textures')
        custom_flags = texture_config.get('flags', [])
        
        # Check if a custom toolchain is specified for this texture group
        texture_toolchain_name = texture_config.get('toolchain')
        if texture_toolchain_name:
            # Temporarily switch toolchain for texture conversion
            texture_toolchain = self.toolchain_manager.get_toolchain(texture_toolchain_name)
            if not texture_toolchain:
                logger.warning(f"Texture toolchain '{texture_toolchain_name}' not found, using current toolchain")
                texture_toolchain = self.current_toolchain
            texture_tool_matcher = ToolMatcher(texture_toolchain)
            texture_cmd_builder = CommandBuilder(texture_toolchain, texture_tool_matcher, self.configuration)
        else:
            texture_tool_matcher = self.tool_matcher
            texture_cmd_builder = self.command_builder
        
        # Expand source patterns
        if isinstance(sources, str):
            sources = self._expand_glob(sources)
        
        # Generate conversion task for each texture
        for source in sources:
            # Find appropriate tool for this texture file
            tool = texture_tool_matcher.find_tool('convert', source)
            if not tool:
                logger.warning(f"No convert tool found for texture: {source}, skipping")
                continue
            
            output_file = f"{texture_output_dir}/{Path(source).stem}{tool.output_extension}"
            
            # Build texture conversion command
            command, _ = texture_cmd_builder.build_command(
                tool=tool,
                source=source,
                output=output_file,
                extra_flags=custom_flags
            )
            
            task = BuildTask(
                task_id=f"texture_{texture_name}_{task_counter:03d}",
                task_type="convert",
                inputs=[TaskInput(path=source)],
                outputs=[output_file],
                dependencies=[setup_task_id],
                command=command,
                platform=self.platform,
                architecture=self.architecture,
                configuration=self.configuration,
                estimated_time=2.0,
                resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=200, disk_mb=10)
            )
            tasks.append(task)
            task_counter += 1
        
        return tasks