"""Toolchain management and command building"""

import logging
import yaml
from pathlib import Path
from dataclasses import dataclass
from typing import Dict, List, Optional, Any

logger = logging.getLogger('buildy.toolchain')

@dataclass
class Tool:
    """Individual tool within a toolchain"""
    name: str
    action: str
    command: str
    input_extensions: List[str]
    output_extension: str
    output_pattern: str = "{name}"
    flags: Dict[str, List[str]] = None
    supports: Dict[str, Any] = None
    
    def __post_init__(self):
        if self.flags is None:
            self.flags = {}
        if self.supports is None:
            self.supports = {}

@dataclass
class ToolchainConfig:
    """Toolchain configuration loaded from YAML"""
    name: str
    description: str
    target_platform: str
    target_architecture: str
    host_platform: str
    host_architecture: str
    execution_type: str
    execution_config: Dict[str, Any]
    tools: Dict[str, Tool]  # tool_name -> Tool object
    
    @staticmethod
    def load(toolchain_file: str) -> 'ToolchainConfig':
        """Load toolchain configuration from YAML"""
        try:
            with open(toolchain_file, 'r') as f:
                data = yaml.safe_load(f)
            
            tc = data['toolchain']
            
            # Parse tools
            tools = {}
            for tool_name, tool_data in tc.get('tools', {}).items():
                # Normalize input_extensions to list
                input_exts = tool_data.get('input_extensions', [])
                if isinstance(input_exts, str):
                    input_exts = [input_exts]
                
                tools[tool_name] = Tool(
                    name=tool_name,
                    action=tool_data.get('action', 'compile'),
                    command=tool_data.get('command', ''),
                    input_extensions=input_exts,
                    output_extension=tool_data.get('output_extension', ''),
                    output_pattern=tool_data.get('output_pattern', '{name}'),
                    flags=tool_data.get('flags', {}),
                    supports=tool_data.get('supports', {})
                )
            
            return ToolchainConfig(
                name=tc['name'],
                description=tc.get('description', ''),
                target_platform=tc['target']['platform'],
                target_architecture=tc['target']['architecture'],
                host_platform=tc['host']['platform'],
                host_architecture=tc['host']['architecture'],
                execution_type=tc['execution']['type'],
                execution_config=tc['execution'],
                tools=tools
            )
        except Exception as e:
            logger.error(f"Failed to load toolchain from {toolchain_file}: {e}")
            raise

class ToolMatcher:
    """Matches files to appropriate tools based on action and extension"""
    
    def __init__(self, toolchain: ToolchainConfig):
        self.toolchain = toolchain
        # Build lookup table: (action, extension) -> Tool
        self._tool_map: Dict[tuple[str, str], Tool] = {}
        self._build_tool_map()
    
    def _build_tool_map(self):
        """Build action+extension lookup map"""
        for tool in self.toolchain.tools.values():
            # Only build map for non-link actions
            # Link tools are handled separately by find_link_tool()
            if tool.action != 'link':
                for ext in tool.input_extensions:
                    key = (tool.action, ext)
                    if key in self._tool_map:
                        existing = self._tool_map[key]
                        logger.error(
                            f"Ambiguous tool definition in toolchain '{self.toolchain.name}': "
                            f"Both '{existing.name}' and '{tool.name}' handle action='{tool.action}' "
                            f"with extension='{ext}'"
                        )
                        raise ValueError(f"Ambiguous tool mapping: {key}")
                    self._tool_map[key] = tool
    
    def find_tool(self, action: str, file_path: str) -> Optional[Tool]:
        """Find tool that matches action and file extension
        
        Args:
            action: The action to perform (e.g., 'compile', 'link', 'convert')
            file_path: Path to the file (extension will be extracted)
        
        Returns:
            Matching Tool or None if no match found
        """
        ext = Path(file_path).suffix
        if not ext:
            return None
        
        key = (action, ext)
        tool = self._tool_map.get(key)
        
        if tool:
            logger.debug(f"Matched {file_path} ({action}) -> tool '{tool.name}'")
        else:
            logger.debug(f"No tool found for action='{action}' extension='{ext}'")
        
        return tool
    
    def find_link_tool(self, output_type: str) -> Optional[Tool]:
        """Find tool for linking based on output type
        
        Args:
            output_type: 'shared_library', 'static_library', or 'executable'
        
        Returns:
            Matching Tool or None
        """
        # Look for tool with action='link' and matching output type
        for tool in self.toolchain.tools.values():
            if tool.action == 'link':
                # Check if tool name or supports indicates it handles this output type
                if output_type in tool.name or tool.supports.get('output_type') == output_type:
                    logger.debug(f"Matched link tool for '{output_type}' -> '{tool.name}'")
                    return tool
        
        logger.warning(f"No link tool found for output_type='{output_type}'")
        return None

class ToolchainManager:
    """Manages toolchain selection and loading"""
    
    def __init__(self, toolchains_dir: str = "toolchains"):
        self.toolchains_dir = Path(toolchains_dir)
        self._toolchains: Dict[str, ToolchainConfig] = {}
        self._load_toolchains()
    
    def _load_toolchains(self):
        """Load all toolchain configurations"""
        if not self.toolchains_dir.exists():
            logger.warning(f"Toolchains directory not found: {self.toolchains_dir}")
            return
        
        for tc_file in self.toolchains_dir.glob("*.yaml"):
            try:
                tc = ToolchainConfig.load(tc_file)
                self._toolchains[tc.name] = tc
                logger.debug(f"Loaded toolchain: {tc.name} - {tc.description}")
            except Exception as e:
                logger.error(f"Failed to load toolchain {tc_file}: {e}")
    
    def get_toolchain(self, name: str) -> Optional[ToolchainConfig]:
        """Get toolchain by name"""
        return self._toolchains.get(name)
    
    def auto_detect(self, platform: str, architecture: str) -> Optional[ToolchainConfig]:
        """Auto-detect best toolchain for platform/architecture"""
        # Try to find matching native toolchain
        for tc in self._toolchains.values():
            if (tc.target_platform == platform and 
                tc.target_architecture == architecture and
                tc.execution_type == 'native'):
                logger.info(f"Auto-detected toolchain: {tc.name}")
                return tc
        
        logger.warning(f"No native toolchain found for {platform}-{architecture}")
        return None
    
    def list_toolchains(self) -> List[tuple[str, str]]:
        """List available toolchains with descriptions"""
        return [(tc.name, tc.description) for tc in self._toolchains.values()]

class CommandBuilder:
    """Builds commands using tool-based approach"""
    
    def __init__(self, toolchain: ToolchainConfig, tool_matcher: ToolMatcher, config_type: str = 'debug'):
        self.toolchain = toolchain
        self.tool_matcher = tool_matcher
        self.config_type = config_type
    
    def build_command(self, tool: Tool, source: str, output: str,
                     defines: List[str] = None, include_dirs: List[str] = None,
                     extra_flags: List[str] = None, **kwargs) -> tuple[str, str]:
        """Build command using tool template
        
        Args:
            tool: The Tool to use
            source: Input file path
            output: Output file path
            defines: Preprocessor defines (if tool supports)
            include_dirs: Include directories (if tool supports)
            extra_flags: Additional flags
            **kwargs: Additional template variables (std, pic, etc.)
        
        Returns:
            tuple: (command, dep_file) - The command and dependency file path (if applicable)
        """
        defines = defines or []
        include_dirs = include_dirs or []
        extra_flags = extra_flags or []
        
        # Normalize extra_flags to list if it's a string
        if isinstance(extra_flags, str):
            extra_flags = [extra_flags] if extra_flags else []
        
        # Get flags for current configuration
        common_flags = tool.flags.get('common', [])
        config_flags = tool.flags.get(self.config_type, [])
        all_flags = common_flags + config_flags + extra_flags
        
        # Build template variables
        template_vars = {
            'input': source,
            'output': output,
            'flags': ' '.join(all_flags),
        }
        
        # Add defines if tool supports them
        if tool.supports.get('defines') and defines:
            define_flag = tool.supports.get('define_flag', '-D')
            template_vars['defines'] = ' '.join(f"{define_flag}{d}" for d in defines)
        else:
            template_vars['defines'] = ''
        
        # Add includes if tool supports them
        if tool.supports.get('includes') and include_dirs:
            include_flag = tool.supports.get('include_flag', '-I')
            template_vars['includes'] = ' '.join(f"{include_flag}{inc}" for inc in include_dirs)
        else:
            template_vars['includes'] = ''
        
        # Add PIC flag if tool supports it and requested
        if tool.supports.get('pic') and kwargs.get('is_shared_library'):
            template_vars['pic'] = tool.supports.get('pic_flag', '-fPIC')
        else:
            template_vars['pic'] = ''
        
        # Handle dependency generation if tool supports it
        dep_file = ''
        if tool.supports.get('dependencies'):
            dep_template = tool.supports.get('dependencies')
            if dep_template:
                dep_file = output.replace(tool.output_extension, '.d')
                template_vars['dep_file'] = dep_file
                template_vars['dep_flags'] = dep_template.format(dep_file=dep_file)
            else:
                template_vars['dep_flags'] = ''
        else:
            template_vars['dep_flags'] = ''
        
        # Add any additional kwargs
        for key, value in kwargs.items():
            if key not in template_vars:
                template_vars[key] = value
        
        # Build command from template
        try:
            command = tool.command.format(**template_vars)
        except KeyError as e:
            logger.error(f"Missing template variable {e} for tool '{tool.name}'")
            raise
        
        # Clean up extra spaces
        command = ' '.join(command.split())
        
        return command, dep_file
    
    def build_link_command(self, tool: Tool, objects: List[str], output: str,
                          lib_dirs: List[str] = None, libs: List[str] = None) -> str:
        """Build link command using tool template
        
        Args:
            tool: The link tool to use
            objects: List of object files to link
            output: Output file path
            lib_dirs: Library search directories
            libs: Library names to link against
        """
        lib_dirs = lib_dirs or []
        libs = libs or []
        
        # Build template variables
        template_vars = {
            'objects': ' '.join(objects),
            'output': output,
        }
        
        # Add library directories if tool supports them
        if tool.supports.get('lib_dirs') and lib_dirs:
            lib_dir_flag = tool.supports.get('lib_dir_flag', '-L')
            template_vars['lib_dirs'] = ' '.join(f"{lib_dir_flag}{d}" for d in lib_dirs)
        else:
            template_vars['lib_dirs'] = ''
        
        # Add libraries if tool supports them
        if tool.supports.get('libs') and libs:
            lib_flag = tool.supports.get('lib_flag', '-l')
            if lib_flag:
                template_vars['libs'] = ' '.join(f"{lib_flag}{lib}" for lib in libs)
            else:
                # No prefix (e.g., MSVC style)
                template_vars['libs'] = ' '.join(libs)
        else:
            template_vars['libs'] = ''
        
        # Get flags for current configuration
        common_flags = tool.flags.get('common', [])
        config_flags = tool.flags.get(self.config_type, [])
        all_flags = common_flags + config_flags
        template_vars['flags'] = ' '.join(all_flags)
        
        # Build command
        try:
            command = tool.command.format(**template_vars)
        except KeyError as e:
            logger.error(f"Missing template variable {e} for link tool '{tool.name}'")
            raise
        
        # Clean up extra spaces
        command = ' '.join(command.split())
        
        return command

