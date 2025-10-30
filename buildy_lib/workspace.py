"""
Workspace management for multi-module projects.

Supports:
- Workspace discovery (walk up directory tree)
- Module discovery (recursive buildy.yaml search)
- Discovery patterns (include/exclude)
- Hierarchical variable environments
"""

import os
import json
import yaml
import logging
from pathlib import Path
from typing import Dict, List, Optional, Any, Set
from dataclasses import dataclass, field, asdict

logger = logging.getLogger('buildy.workspace')


@dataclass
class ModuleInfo:
    """Information about a discovered module"""
    path: Path  # Path to buildy.yaml file
    relative_path: str  # Path relative to workspace root
    config: Dict[str, Any]  # Parsed YAML content
    targets: List[str] = field(default_factory=list)  # Target names in this module


@dataclass
class WorkspaceConfig:
    """Workspace configuration from root buildy.yaml"""
    root_dir: Path
    discover_patterns: List[str] = field(default_factory=lambda: ["**/buildy.yaml"])
    exclude_patterns: List[str] = field(default_factory=lambda: [
        ".buildy_cache/**",
        "venv/**",
        ".git/**"
    ])
    variables: Dict[str, Any] = field(default_factory=dict)
    raw_config: Dict[str, Any] = field(default_factory=dict)


class Workspace:
    """Manages multi-module workspace with buildy.yaml files"""
    
    def __init__(self, root_dir: Optional[Path] = None):
        """
        Initialize workspace.
        
        Args:
            root_dir: Workspace root directory. If None, searches up from cwd.
        """
        self.root_dir = root_dir or self._find_workspace_root()
        self.config = self._load_workspace_config()
        self.modules: Dict[str, ModuleInfo] = {}  # relative_path -> ModuleInfo
        self._discovered = False
    
    @staticmethod
    def _find_workspace_root(start_dir: Optional[Path] = None) -> Path:
        """
        Find workspace root by walking up directory tree looking for buildy.yaml.
        
        Args:
            start_dir: Directory to start search from (default: cwd)
            
        Returns:
            Path to workspace root directory
            
        Raises:
            FileNotFoundError: If no buildy.yaml found
        """
        current = Path(start_dir or os.getcwd()).resolve()
        
        # Walk up directory tree
        while True:
            candidate = current / "buildy.yaml"
            if candidate.exists():
                logger.debug(f"Found workspace root: {current}")
                return current
            
            # Check if we've reached filesystem root
            parent = current.parent
            if parent == current:
                raise FileNotFoundError(
                    "No buildy.yaml found in current directory or any parent directory. "
                    "Please create a buildy.yaml file in your project root."
                )
            
            current = parent
    
    def _load_workspace_config(self) -> WorkspaceConfig:
        """Load workspace configuration from root buildy.yaml"""
        config_file = self.root_dir / "buildy.yaml"
        
        if not config_file.exists():
            raise FileNotFoundError(f"Workspace config not found: {config_file}")
        
        try:
            with open(config_file, 'r') as f:
                raw_config = yaml.safe_load(f) or {}
            
            # Extract workspace section
            workspace_section = raw_config.get('workspace', {})
            
            # Get discovery patterns (default to discover all if not specified)
            discover = workspace_section.get('discover')
            if discover is None:
                # Default: discover all buildy.yaml files
                discover_patterns = ["**/buildy.yaml"]
            else:
                # Use specified patterns
                discover_patterns = discover if isinstance(discover, list) else [discover]
            
            # Get exclude patterns (always exclude cache and common dirs)
            exclude = workspace_section.get('exclude', [])
            if not isinstance(exclude, list):
                exclude = [exclude] if exclude else []
            
            # Add default excludes
            default_excludes = [".buildy_cache/**", "venv/**", ".git/**", "**/.git/**"]
            exclude_patterns = default_excludes + exclude
            
            # Get workspace variables
            variables = raw_config.get('variables', {})
            
            config = WorkspaceConfig(
                root_dir=self.root_dir,
                discover_patterns=discover_patterns,
                exclude_patterns=exclude_patterns,
                variables=variables,
                raw_config=raw_config
            )
            
            logger.info(f"Loaded workspace from {self.root_dir}")
            logger.debug(f"Discovery patterns: {discover_patterns}")
            logger.debug(f"Exclude patterns: {exclude_patterns}")
            
            return config
            
        except yaml.YAMLError as e:
            raise ValueError(f"Invalid YAML in {config_file}: {e}")
        except Exception as e:
            raise RuntimeError(f"Failed to load workspace config: {e}")
    
    def discover_modules(self, force: bool = False) -> Dict[str, ModuleInfo]:
        """
        Discover all buildy.yaml module files in workspace.
        
        Args:
            force: Force re-discovery even if already discovered
            
        Returns:
            Dictionary of relative_path -> ModuleInfo
        """
        if self._discovered and not force:
            return self.modules
        
        logger.info("Discovering modules in workspace...")
        self.modules = {}
        
        # Find all buildy.yaml files
        discovered_files = self._find_module_files(self.root_dir, self.config)
        
        # Load each module
        for module_path in discovered_files:
            try:
                relative_path = str(module_path.relative_to(self.root_dir))
                module_dir = str(module_path.parent.relative_to(self.root_dir))
                
                # Root buildy.yaml is module "."
                if module_path == self.root_dir / "buildy.yaml":
                    module_dir = "."
                
                module_info = self._load_module(module_path, module_dir)
                self.modules[module_dir] = module_info
                
                logger.debug(f"Discovered module: {module_dir} ({len(module_info.targets)} targets)")
                
            except Exception as e:
                logger.warning(f"Failed to load module {module_path}: {e}")
        
        self._discovered = True
        logger.info(f"Discovered {len(self.modules)} modules with {self._count_total_targets()} total targets")
        
        return self.modules
    
    def _find_module_files(self, root: Path, config: WorkspaceConfig) -> List[Path]:
        """
        Find all buildy.yaml files matching discovery patterns.
        
        Args:
            root: Root directory to search from
            config: Workspace configuration with patterns
            
        Returns:
            List of paths to buildy.yaml files
        """
        import fnmatch
        
        discovered = set()
        
        # Walk directory tree
        for dirpath, dirnames, filenames in os.walk(root):
            current_path = Path(dirpath)
            relative_path = current_path.relative_to(root)
            
            # Check if this directory should be excluded
            should_exclude = False
            for exclude_pattern in config.exclude_patterns:
                # Remove ** prefix for matching
                pattern = exclude_pattern.replace("**/", "")
                if fnmatch.fnmatch(str(relative_path), pattern) or \
                   fnmatch.fnmatch(str(relative_path) + "/", pattern):
                    should_exclude = True
                    break
            
            if should_exclude:
                # Don't descend into excluded directories
                dirnames[:] = []
                continue
            
            # Check for buildy.yaml in this directory
            buildy_file = current_path / "buildy.yaml"
            if buildy_file.exists():
                # Check if it matches any discovery pattern
                for pattern in config.discover_patterns:
                    # Remove ** prefix for matching
                    pattern_clean = pattern.replace("**/", "")
                    file_relative = str(buildy_file.relative_to(root))
                    
                    if fnmatch.fnmatch(file_relative, pattern_clean) or \
                       fnmatch.fnmatch(file_relative, pattern):
                        discovered.add(buildy_file)
                        break
        
        return sorted(discovered)
    
    def _load_module(self, module_path: Path, relative_dir: str) -> ModuleInfo:
        """
        Load a module configuration file.
        
        Args:
            module_path: Path to buildy.yaml file
            relative_dir: Directory path relative to workspace root
            
        Returns:
            ModuleInfo object
        """
        try:
            with open(module_path, 'r') as f:
                config = yaml.safe_load(f) or {}
            
            # Extract target names from both 'tasks' and 'targets' sections
            targets = []
            for task_section in ['tasks', 'targets']:
                if task_section in config:
                    for target in config.get(task_section, []):
                        if isinstance(target, dict) and 'name' in target:
                            targets.append(target['name'])
            
            # Check for module-level discover patterns
            if 'discover' in config:
                # Module can refine discovery for its subdirectories
                logger.debug(f"Module {relative_dir} has custom discovery patterns")
            
            return ModuleInfo(
                path=module_path,
                relative_path=relative_dir,
                config=config,
                targets=targets
            )
            
        except yaml.YAMLError as e:
            raise ValueError(f"Invalid YAML in {module_path}: {e}")
        except Exception as e:
            raise RuntimeError(f"Failed to load module {module_path}: {e}")
    
    def _count_total_targets(self) -> int:
        """Count total number of targets across all modules"""
        return sum(len(module.targets) for module in self.modules.values())
    
    def get_module(self, relative_path: str) -> Optional[ModuleInfo]:
        """
        Get module by relative path.
        
        Args:
            relative_path: Path relative to workspace root
            
        Returns:
            ModuleInfo or None if not found
        """
        if not self._discovered:
            self.discover_modules()
        
        return self.modules.get(relative_path)
    
    def list_modules(self) -> List[str]:
        """
        Get list of all module paths.
        
        Returns:
            List of relative paths to modules
        """
        if not self._discovered:
            self.discover_modules()
        
        return sorted(self.modules.keys())
    
    def list_all_targets(self) -> Dict[str, List[str]]:
        """
        Get all targets organized by module.
        
        Returns:
            Dictionary of module_path -> list of target names
        """
        if not self._discovered:
            self.discover_modules()
        
        return {
            module_path: module.targets
            for module_path, module in self.modules.items()
        }
    
    @staticmethod
    def is_workspace_root(directory: Path) -> bool:
        """
        Check if directory is a workspace root.
        
        Args:
            directory: Directory to check
            
        Returns:
            True if directory contains buildy.yaml
        """
        return (directory / "buildy.yaml").exists()
    
    @staticmethod
    def discover(start_dir: str) -> Optional['Workspace']:
        """
        Discover workspace starting from a directory.
        
        Args:
            start_dir: Directory to start search from
            
        Returns:
            Workspace instance if found, None otherwise
        """
        try:
            root = Workspace._find_workspace_root(Path(start_dir))
            return Workspace(root)
        except FileNotFoundError:
            return None
    
    def save_discovery_cache(self, cache_dir: Path) -> None:
        """
        Save workspace discovery results to cache.
        
        Args:
            cache_dir: Cache directory (typically .buildy_cache)
        """
        cache_file = cache_dir / "workspace.json"
        cache_dir.mkdir(parents=True, exist_ok=True)
        
        # Serialize workspace info
        cache_data = {
            "root": str(self.root_dir),
            "config": {
                "discover_patterns": self.config.discover_patterns,
                "exclude_patterns": self.config.exclude_patterns,
                "variables": self.config.variables
            },
            "modules": {
                rel_path: {
                    "path": str(module.path),
                    "relative_path": module.relative_path,
                    "targets": module.targets,
                    # Don't cache full config, it will be reloaded
                }
                for rel_path, module in self.modules.items()
            }
        }
        
        with open(cache_file, 'w') as f:
            json.dump(cache_data, f, indent=2)
        
        logger.debug(f"Saved workspace discovery cache to {cache_file}")
    
    @staticmethod
    def load_discovery_cache(cache_dir: Path) -> Optional['Workspace']:
        """
        Load workspace discovery results from cache.
        
        Args:
            cache_dir: Cache directory (typically .buildy_cache)
            
        Returns:
            Workspace instance if cache is valid, None otherwise
        """
        cache_file = cache_dir / "workspace.json"
        
        if not cache_file.exists():
            return None
        
        try:
            with open(cache_file, 'r') as f:
                cache_data = json.load(f)
            
            root_dir = Path(cache_data["root"])
            
            # Validate that workspace root still exists
            if not Workspace.is_workspace_root(root_dir):
                logger.debug("Cached workspace root no longer valid")
                return None
            
            # Create workspace instance
            workspace = Workspace(root_dir)
            
            # Restore modules (but reload configs to ensure freshness)
            for rel_path, module_data in cache_data["modules"].items():
                module_path = Path(module_data["path"])
                
                # Validate module file still exists
                if not module_path.exists():
                    logger.debug(f"Cached module no longer exists: {module_path}")
                    return None
                
                # Reload module config
                try:
                    with open(module_path, 'r') as f:
                        config = yaml.safe_load(f) or {}
                except Exception as e:
                    logger.debug(f"Failed to reload module config: {e}")
                    return None
                
                # Extract targets
                targets = []
                for task_section in ['tasks', 'targets']:
                    if task_section in config:
                        for task in config[task_section]:
                            if isinstance(task, dict) and 'name' in task:
                                targets.append(task['name'])
                
                workspace.modules[rel_path] = ModuleInfo(
                    path=module_path,
                    relative_path=module_data["relative_path"],
                    config=config,
                    targets=targets
                )
            
            workspace._discovered = True
            logger.debug(f"Loaded workspace from cache: {len(workspace.modules)} modules")
            return workspace
            
        except Exception as e:
            logger.debug(f"Failed to load workspace cache: {e}")
            return None

