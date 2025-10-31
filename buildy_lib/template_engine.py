"""Build template engine for expanding universal build templates into tasks"""

import logging
import yaml
from pathlib import Path
from typing import Dict, List, Optional, Any
from glob import glob as glob_func

from .models import BuildTask, TaskInput, ResourceRequirements
from .constants import DEFAULT_COMPILE_TIME_SECONDS, DEFAULT_LINK_TIME_SECONDS

logger = logging.getLogger('buildy.template_engine')

class BuildTemplateEngine:
    """Expands universal build templates into concrete tasks"""
    
    def __init__(self, templates_file: str = "data/templates/buildy_templates.yaml"):
        self.templates_file = Path(templates_file)
        self.templates = self._load_templates()
    
    def _load_templates(self) -> Dict[str, Any]:
        """Load build templates from YAML file"""
        if not self.templates_file.exists():
            logger.warning(f"Templates file not found: {self.templates_file}")
            return {}
        
        try:
            with open(self.templates_file, 'r') as f:
                data = yaml.safe_load(f)
            return data.get('templates', {})
        except Exception as e:
            logger.error(f"Failed to load templates: {e}")
            return {}
    
    def get_template(self, template_name: str) -> Optional[Dict[str, Any]]:
        """Get a template by name"""
        return self.templates.get(template_name)
    
    def list_templates(self) -> List[tuple[str, str]]:
        """List available templates with descriptions"""
        return [(name, tmpl.get('description', '')) 
                for name, tmpl in self.templates.items()]
    
    def expand_template(self, template_name: str, item_config: Dict[str, Any],
                       merged_config: Dict[str, Any], output_dir: str,
                       setup_task_id: str, task_counter: int,
                       tool_matcher: 'ToolMatcher', command_builder: 'CommandBuilder',
                       platform: str, architecture: str, configuration: str,
                       existing_tasks: List['BuildTask'] = None) -> List['BuildTask']:
        """Expand a template into concrete build tasks
        
        Args:
            template_name: Name of template to expand
            item_config: Configuration for this specific item (library/executable/etc)
            merged_config: Merged global configuration
            output_dir: Base output directory
            setup_task_id: ID of setup task to depend on
            task_counter: Starting task counter
            tool_matcher: Tool matcher for finding appropriate tools
            command_builder: Command builder for generating commands
            platform: Target platform
            architecture: Target architecture
            configuration: Build configuration (debug/release)
            existing_tasks: List of existing tasks (for dependency resolution)
        
        Returns:
            List of generated BuildTask objects
        """
        existing_tasks = existing_tasks or []
        template = self.get_template(template_name)
        if not template:
            raise ValueError(f"Template '{template_name}' not found")
        
        tasks = []
        step_results = {}  # Store results from each step for reference
        
        # Build context for template variable resolution
        context = {
            'item': item_config,
            'config': merged_config,
            'output_dir': output_dir,
            'platform': platform,
            'architecture': architecture,
            'configuration': configuration,
        }
        
        for step in template.get('steps', []):
            step_name = step.get('name', 'unnamed')
            action = step.get('action')
            
            if step.get('for_each'):
                # Generate multiple tasks (one per source file)
                step_tasks = self._expand_foreach_step(
                    step, context, item_config, merged_config, output_dir,
                    setup_task_id, task_counter, tool_matcher, command_builder,
                    platform, architecture, configuration
                )
                tasks.extend(step_tasks)
                task_counter += len(step_tasks)
                
                # Store results for later steps to reference
                step_results[step_name] = {
                    'tasks': step_tasks,
                    'task_ids': [t.task_id for t in step_tasks],
                    'outputs': [out for t in step_tasks for out in t.outputs]
                }
            else:
                # Generate single task
                step_task = self._expand_single_step(
                    step, context, step_results, item_config, merged_config,
                    output_dir, task_counter, tool_matcher, command_builder,
                    platform, architecture, configuration, existing_tasks
                )
                if step_task:
                    tasks.append(step_task)
                    task_counter += 1
                    
                    step_results[step_name] = {
                        'tasks': [step_task],
                        'task_ids': [step_task.task_id],
                        'outputs': step_task.outputs
                    }
        
        return tasks
    
    def _expand_foreach_step(self, step: Dict[str, Any], context: Dict[str, Any],
                            item_config: Dict[str, Any], merged_config: Dict[str, Any],
                            output_dir: str, setup_task_id: str, task_counter: int,
                            tool_matcher: 'ToolMatcher', command_builder: 'CommandBuilder',
                            platform: str, architecture: str, configuration: str) -> List['BuildTask']:
        """Expand a for_each step into multiple tasks"""
        tasks = []
        sources = item_config.get('sources', [])
        
        if isinstance(sources, str):
            # Expand glob pattern
            from glob import glob as glob_func
            sources = sorted(glob_func(sources, recursive=True))
        
        for source in sources:
            source_path = Path(source)
            
            # Find appropriate tool for this source file
            action = step.get('action')
            tool = tool_matcher.find_tool(action, source)
            if not tool:
                logger.warning(f"No {action} tool found for {source}, skipping")
                continue
            
            # Build context for this iteration
            iter_context = {
                **context,
                'source': source,
                'source_stem': source_path.stem,
                'tool': {
                    'output_ext': tool.output_extension,
                    'output_pattern': tool.output_pattern
                }
            }
            
            # Resolve output path
            output_template = step.get('output', '')
            output = self._resolve_template_string(output_template, iter_context)
            
            # Get tool parameters
            tool_params = step.get('tool_params', {})
            resolved_params = self._resolve_tool_params(tool_params, iter_context)
            
            # Build command
            command, dep_file = command_builder.build_command(
                tool=tool,
                source=source,
                output=output,
                **resolved_params
            )
            
            # Build outputs list
            outputs = [output]
            if dep_file:
                outputs.append(dep_file)
            
            # Create task
            task_name = item_config.get('name', 'unnamed')
            task = BuildTask(
                task_id=f"{action}_{task_name}_{task_counter:03d}",
                task_type=action,
                inputs=[TaskInput(path=source)],
                outputs=outputs,
                dependencies=[setup_task_id],
                command=command,
                platform=platform,
                architecture=architecture,
                configuration=configuration,
                estimated_time=DEFAULT_COMPILE_TIME_SECONDS,
                resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=200, disk_mb=15)
            )
            tasks.append(task)
            task_counter += 1
        
        return tasks
    
    def _expand_single_step(self, step: Dict[str, Any], context: Dict[str, Any],
                           step_results: Dict[str, Any], item_config: Dict[str, Any],
                           merged_config: Dict[str, Any], output_dir: str,
                           task_counter: int, tool_matcher: 'ToolMatcher',
                           command_builder: 'CommandBuilder', platform: str,
                           architecture: str, configuration: str,
                           existing_tasks: List['BuildTask']) -> Optional['BuildTask']:
        """Expand a single (non-foreach) step into a task"""
        action = step.get('action')
        output_type = step.get('output_type')
        
        # Find appropriate tool
        if output_type:
            tool = tool_matcher.find_link_tool(output_type)
        else:
            # For non-link actions, we'd need a source file to match
            # This is a limitation - single steps without for_each are typically link steps
            tool = tool_matcher.find_link_tool(output_type) if output_type else None
        
        if not tool:
            logger.warning(f"No tool found for action={action}, output_type={output_type}")
            return None
        
        # Update context with tool info
        step_context = {
            **context,
            **step_results,
            'tool': {
                'output_ext': tool.output_extension,
                'output_pattern': tool.output_pattern.format(name=item_config.get('name', 'output'))
            }
        }
        
        # Resolve output path
        output_template = step.get('output', '')
        output = self._resolve_template_string(output_template, step_context)
        
        # Collect inputs from previous step
        inputs_ref = step.get('inputs', '')
        inputs = self._resolve_reference(inputs_ref, step_results)
        if not isinstance(inputs, list):
            inputs = [inputs] if inputs else []
        
        # Filter inputs to only include files matching tool's input extensions
        filtered_inputs = [inp for inp in inputs 
                          if any(inp.endswith(ext) for ext in tool.input_extensions)]
        
        # Resolve dependencies
        depends_on_ref = step.get('depends_on', [])
        if isinstance(depends_on_ref, str):
            depends_on_ref = [depends_on_ref]
        
        dependencies = []
        for dep_ref in depends_on_ref:
            resolved_deps = self._resolve_reference(dep_ref, step_results)
            if isinstance(resolved_deps, list):
                dependencies.extend(resolved_deps)
            elif resolved_deps:
                dependencies.append(resolved_deps)
        
        # Handle library dependencies for executables
        lib_dirs = []
        lib_names = []
        if output_type == 'executable':
            depends_on_libs = item_config.get('depends_on', [])
            if depends_on_libs:
                lib_dirs.append(f"{output_dir}/lib")
                
                # Build dependency graph for libraries to determine correct link order
                # Libraries must be in reverse dependency order (dependents before dependencies)
                lib_deps = {}  # lib_name -> list of dependencies
                
                for dep in depends_on_libs:
                    # Extract the target name (handle scoped references)
                    if ':' in dep:
                        lib_name = dep.split(':')[-1]
                    else:
                        lib_name = dep
                    
                    # Find this library's dependencies from existing_tasks
                    lib_dependencies = []
                    for task in existing_tasks:
                        if task.task_type == 'link' and lib_name in task.task_id:
                            # This is the library's link task, check its dependencies
                            for task_dep in task.dependencies:
                                # If dependency is another library link task, extract its name
                                if task_dep.startswith('link_') and task_dep != task.task_id:
                                    # Extract library name from task_id like "link_engine_core_005"
                                    parts = task_dep.split('_')
                                    if len(parts) >= 3:
                                        dep_lib_name = '_'.join(parts[1:-1])
                                        if dep_lib_name in [d.split(':')[-1] if ':' in d else d for d in depends_on_libs]:
                                            lib_dependencies.append(dep_lib_name)
                            break
                    
                    lib_deps[lib_name] = lib_dependencies
                    dependencies.append(dep)
                
                # Topological sort to get correct link order (reverse dependency order)
                lib_names = self._topological_sort_libs(lib_deps)
                
                logger.debug(f"Library link order: {lib_names}")
        
        # Get tool parameters
        tool_params = step.get('tool_params', {})
        resolved_params = self._resolve_tool_params(tool_params, step_context)
        
        # Override with library linking info
        if lib_dirs:
            resolved_params['lib_dirs'] = lib_dirs
        if lib_names:
            resolved_params['libs'] = lib_names
        
        # Build command
        command = command_builder.build_link_command(
            tool=tool,
            objects=filtered_inputs,
            output=output,
            **resolved_params
        )
        
        # Create task
        task_name = item_config.get('name', 'unnamed')
        task = BuildTask(
            task_id=f"{action}_{task_name}_{task_counter:03d}",
            task_type=action,
            inputs=[TaskInput(path=inp) for inp in filtered_inputs],
            outputs=[output],
            dependencies=dependencies,
            command=command,
            platform=platform,
            architecture=architecture,
            configuration=configuration,
            estimated_time=DEFAULT_LINK_TIME_SECONDS,
            resource_requirements=ResourceRequirements(cpu_cores=1, memory_mb=150, disk_mb=25)
        )
        
        return task
    
    def _topological_sort_libs(self, lib_deps: Dict[str, List[str]]) -> List[str]:
        """
        Topologically sort libraries in reverse dependency order.
        Libraries that depend on others come BEFORE their dependencies.
        
        Args:
            lib_deps: Dictionary mapping library names to their dependencies
            
        Returns:
            List of library names in correct link order
        """
        # Build in-degree map (how many libraries depend on each library)
        in_degree = {lib: 0 for lib in lib_deps}
        for lib, deps in lib_deps.items():
            for dep in deps:
                if dep in in_degree:
                    in_degree[dep] += 1
        
        # Start with libraries that have no dependents (highest in dependency tree)
        queue = [lib for lib, degree in in_degree.items() if degree == 0]
        result = []
        
        while queue:
            # Sort for deterministic output
            queue.sort()
            lib = queue.pop(0)
            result.append(lib)
            
            # Remove this library from the graph
            for dep in lib_deps[lib]:
                if dep in in_degree:
                    in_degree[dep] -= 1
                    if in_degree[dep] == 0:
                        queue.append(dep)
        
        # Check for cycles
        if len(result) != len(lib_deps):
            logger.warning(f"Circular dependency detected in libraries, using original order")
            return list(lib_deps.keys())
        
        return result
    
    def _resolve_template_string(self, template: str, context: Dict[str, Any]) -> Any:
        """Resolve template variables in a string
        
        Returns the resolved value - may be a string, list, or other type
        """
        if not template:
            return ""
        
        # Check if the entire template is just a single reference
        if template.startswith('{') and template.endswith('}') and template.count('{') == 1:
            ref = template.strip('{}')
            parts = ref.split('.')
            obj = context
            for part in parts:
                if isinstance(obj, dict):
                    obj = obj.get(part)
                else:
                    break
            if obj is not None:
                return obj
        
        # Otherwise do string replacement
        result = template
        for key, value in context.items():
            if isinstance(value, dict):
                for subkey, subvalue in value.items():
                    if not isinstance(subvalue, (list, dict)):
                        result = result.replace(f"{{{key}.{subkey}}}", str(subvalue))
            elif not isinstance(value, (list, dict)):
                result = result.replace(f"{{{key}}}", str(value))
        
        return result
    
    def _resolve_reference(self, ref: str, step_results: Dict[str, Any]) -> Any:
        """Resolve a reference to previous step results"""
        if not ref or not isinstance(ref, str):
            return ref
        
        # Remove curly braces if present
        ref = ref.strip('{}')
        
        # Parse reference like "compile.outputs" or "compile.task_ids"
        parts = ref.split('.')
        if len(parts) < 2:
            return ref
        
        step_name = parts[0]
        attr_name = parts[1]
        
        if step_name in step_results:
            return step_results[step_name].get(attr_name, [])
        
        return []
    
    def _resolve_tool_params(self, params: Dict[str, Any], context: Dict[str, Any]) -> Dict[str, Any]:
        """Resolve tool parameters from template"""
        resolved = {}
        
        for key, value in params.items():
            if isinstance(value, str):
                resolved_value = self._resolve_template_string(value, context)
                resolved[key] = resolved_value
            elif isinstance(value, list):
                resolved[key] = [self._resolve_template_string(v, context) if isinstance(v, str) else v 
                               for v in value]
            else:
                resolved[key] = value
        
        return resolved