"""
Target registry for workspace-wide target management and dependency resolution.

Supports three dependency syntaxes:
1. Simple name: "mylib" - searches workspace
2. Scoped name: "src/common:utils" - path:target
3. Local reference: ":helper" - same module
"""

import logging
from typing import Dict, List, Optional, Set, Tuple
from dataclasses import dataclass
from pathlib import Path

from .workspace import Workspace, ModuleInfo

logger = logging.getLogger('buildy.target_registry')


@dataclass
class TargetReference:
    """Reference to a target in the workspace"""
    name: str  # Target name
    module_path: str  # Module path relative to workspace root
    module_info: ModuleInfo  # Module containing this target
    
    @property
    def full_name(self) -> str:
        """Get fully qualified target name (module:target)"""
        return f"{self.module_path}:{self.name}"


class AmbiguousTargetError(Exception):
    """Raised when a target name matches multiple targets"""
    
    def __init__(self, target_name: str, matches: List[TargetReference]):
        self.target_name = target_name
        self.matches = matches
        
        # Build helpful error message
        match_list = "\n".join(f"    - {ref.full_name}" for ref in matches)
        suggestion = f"Use scoped name to disambiguate:\n  dependencies:\n    - {matches[0].full_name}"
        
        message = (
            f"Ambiguous dependency '{target_name}'\n"
            f"  Found in:\n{match_list}\n\n"
            f"  {suggestion}"
        )
        super().__init__(message)


class TargetNotFoundError(Exception):
    """Raised when a target cannot be found"""
    
    def __init__(self, target_name: str, context: Optional[str] = None):
        self.target_name = target_name
        self.context = context
        
        message = f"Target '{target_name}' not found"
        if context:
            message += f" (referenced from {context})"
        
        super().__init__(message)


class TargetRegistry:
    """
    Registry of all targets in a workspace.
    
    Provides:
    - Target lookup by name
    - Dependency resolution
    - Ambiguity detection
    """
    
    def __init__(self, workspace: Workspace):
        """
        Initialize target registry.
        
        Args:
            workspace: Workspace to manage targets for
        """
        self.workspace = workspace
        self._targets_by_name: Dict[str, List[TargetReference]] = {}
        self._targets_by_module: Dict[str, Dict[str, TargetReference]] = {}
        self._initialized = False
    
    def initialize(self):
        """
        Initialize registry by discovering and indexing all targets.
        
        This must be called before using the registry.
        """
        if self._initialized:
            return
        
        logger.info("Initializing target registry...")
        
        # Discover all modules
        modules = self.workspace.discover_modules()
        
        # Index all targets
        for module_path, module_info in modules.items():
            self._targets_by_module[module_path] = {}
            
            for target_name in module_info.targets:
                # Create target reference
                ref = TargetReference(
                    name=target_name,
                    module_path=module_path,
                    module_info=module_info
                )
                
                # Index by module
                self._targets_by_module[module_path][target_name] = ref
                
                # Index by name (for simple name lookup)
                if target_name not in self._targets_by_name:
                    self._targets_by_name[target_name] = []
                self._targets_by_name[target_name].append(ref)
        
        self._initialized = True
        logger.info(f"Indexed {len(self._targets_by_name)} unique target names")
    
    def resolve_dependency(self, 
                          dep_string: str, 
                          current_module: Optional[str] = None) -> TargetReference:
        """
        Resolve a dependency string to a target reference.
        
        Supports three syntaxes:
        1. Simple name: "mylib" - searches workspace
        2. Scoped name: "src/common:utils" - path:target
        3. Local reference: ":helper" - same module
        
        Args:
            dep_string: Dependency string to resolve
            current_module: Module path where dependency is referenced (for :local)
            
        Returns:
            TargetReference for the resolved target
            
        Raises:
            TargetNotFoundError: If target not found
            AmbiguousTargetError: If simple name matches multiple targets
            ValueError: If syntax is invalid
        """
        if not self._initialized:
            self.initialize()
        
        # Local reference: ":target"
        if dep_string.startswith(':'):
            return self._resolve_local(dep_string[1:], current_module)
        
        # Scoped reference: "path:target"
        elif ':' in dep_string:
            return self._resolve_scoped(dep_string)
        
        # Simple name: "target"
        else:
            return self._resolve_simple(dep_string, current_module)
    
    def _resolve_local(self, target_name: str, current_module: Optional[str]) -> TargetReference:
        """
        Resolve local reference (:target) within same module.
        
        Args:
            target_name: Target name (without :)
            current_module: Module path where reference is made
            
        Returns:
            TargetReference
            
        Raises:
            ValueError: If current_module not provided
            TargetNotFoundError: If target not found in module
        """
        if current_module is None:
            raise ValueError(
                f"Local reference ':{target_name}' used but current module unknown. "
                "Local references can only be used within a module."
            )
        
        # Look up in current module
        module_targets = self._targets_by_module.get(current_module, {})
        if target_name in module_targets:
            logger.debug(f"Resolved local reference :{target_name} to {current_module}:{target_name}")
            return module_targets[target_name]
        
        raise TargetNotFoundError(
            target_name,
            context=f"local reference in module '{current_module}'"
        )
    
    def _resolve_scoped(self, dep_string: str) -> TargetReference:
        """
        Resolve scoped reference (path:target).
        
        Args:
            dep_string: Scoped dependency string (e.g., "src/common:utils")
            
        Returns:
            TargetReference
            
        Raises:
            TargetNotFoundError: If target not found at specified path
        """
        if dep_string.count(':') != 1:
            raise ValueError(
                f"Invalid scoped reference '{dep_string}'. "
                "Expected format: 'path:target' (exactly one colon)"
            )
        
        module_path, target_name = dep_string.split(':', 1)
        
        # Normalize path (remove leading/trailing slashes)
        module_path = module_path.strip('/')
        
        # Look up in specified module
        module_targets = self._targets_by_module.get(module_path, {})
        if target_name in module_targets:
            logger.debug(f"Resolved scoped reference {dep_string}")
            return module_targets[target_name]
        
        # Target not found - provide helpful error
        if module_path not in self._targets_by_module:
            raise TargetNotFoundError(
                target_name,
                context=f"module '{module_path}' does not exist"
            )
        else:
            available = list(module_targets.keys())
            raise TargetNotFoundError(
                target_name,
                context=f"module '{module_path}' (available targets: {available})"
            )
    
    def _resolve_simple(self, target_name: str, current_module: Optional[str]) -> TargetReference:
        """
        Resolve simple name reference by searching workspace.
        
        Search order:
        1. Current module (if provided)
        2. Parent modules (walking up directory tree)
        3. All other modules
        
        Args:
            target_name: Simple target name
            current_module: Module where reference is made (optional)
            
        Returns:
            TargetReference
            
        Raises:
            TargetNotFoundError: If target not found
            AmbiguousTargetError: If multiple matches found
        """
        # Get all matches
        matches = self._targets_by_name.get(target_name, [])
        
        if not matches:
            raise TargetNotFoundError(target_name)
        
        # If only one match, return it
        if len(matches) == 1:
            logger.debug(f"Resolved simple reference {target_name} to {matches[0].full_name}")
            return matches[0]
        
        # Multiple matches - try to resolve by proximity
        if current_module:
            # 1. Check current module first
            for match in matches:
                if match.module_path == current_module:
                    logger.debug(f"Resolved simple reference {target_name} to {match.full_name} (current module)")
                    return match
            
            # 2. Check parent modules (walk up directory tree)
            current_parts = Path(current_module).parts
            for i in range(len(current_parts) - 1, 0, -1):
                parent_path = str(Path(*current_parts[:i]))
                for match in matches:
                    if match.module_path == parent_path:
                        logger.debug(f"Resolved simple reference {target_name} to {match.full_name} (parent module)")
                        return match
        
        # Still ambiguous - error with suggestions
        raise AmbiguousTargetError(target_name, matches)
    
    def get_target(self, module_path: str, target_name: str) -> Optional[TargetReference]:
        """
        Get a specific target by module and name.
        
        Args:
            module_path: Module path relative to workspace root
            target_name: Target name
            
        Returns:
            TargetReference or None if not found
        """
        if not self._initialized:
            self.initialize()
        
        return self._targets_by_module.get(module_path, {}).get(target_name)
    
    def list_targets(self, module_path: Optional[str] = None) -> List[TargetReference]:
        """
        List all targets, optionally filtered by module.
        
        Args:
            module_path: Module to filter by (None for all)
            
        Returns:
            List of TargetReference objects
        """
        if not self._initialized:
            self.initialize()
        
        if module_path:
            return list(self._targets_by_module.get(module_path, {}).values())
        else:
            all_targets = []
            for module_targets in self._targets_by_module.values():
                all_targets.extend(module_targets.values())
            return all_targets
    
    def get_target_names(self) -> Set[str]:
        """
        Get set of all target names in workspace.
        
        Returns:
            Set of target names
        """
        if not self._initialized:
            self.initialize()
        
        return set(self._targets_by_name.keys())
    
    def find_targets_by_name(self, target_name: str) -> List[TargetReference]:
        """
        Find all targets with given name.
        
        Args:
            target_name: Target name to search for
            
        Returns:
            List of matching TargetReference objects
        """
        if not self._initialized:
            self.initialize()
        
        return self._targets_by_name.get(target_name, [])

