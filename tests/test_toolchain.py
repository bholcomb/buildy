"""Tests for ToolMatcher and toolchain system"""

import pytest
from pathlib import Path
from buildy_lib.toolchain import Tool, ToolchainConfig, ToolMatcher, ToolchainManager


class TestTool:
    """Test Tool dataclass"""
    
    def test_tool_creation(self):
        """Test creating a Tool instance"""
        tool = Tool(
            name='cpp_compile',
            action='compile',
            input_extensions=['.cpp', '.cc'],
            output_extension='.o',
            output_pattern='{name}.o',
            command='g++ -c {source} -o {output}',
            flags={'debug': ['-g']}
        )
        
        assert tool.name == 'cpp_compile'
        assert tool.action == 'compile'
        assert '.cpp' in tool.input_extensions
        assert tool.output_extension == '.o'


class TestToolMatcher:
    """Test ToolMatcher class"""
    
    def test_find_compile_tool_by_extension(self, sample_toolchain_config):
        """Test finding compile tool by file extension"""
        config = ToolchainConfig.from_dict(sample_toolchain_config)
        matcher = ToolMatcher(config)
        
        tool = matcher.find_tool('compile', 'main.cpp')
        
        assert tool is not None
        assert tool.action == 'compile'
        assert tool.name == 'cpp_compile'
    
    def test_find_tool_no_match(self, sample_toolchain_config):
        """Test that None is returned when no tool matches"""
        config = ToolchainConfig.from_dict(sample_toolchain_config)
        matcher = ToolMatcher(config)
        
        tool = matcher.find_tool('compile', 'main.py')
        
        assert tool is None
    
    def test_find_link_tool_by_output_type(self, sample_toolchain_config):
        """Test finding link tool by output type"""
        config = ToolchainConfig.from_dict(sample_toolchain_config)
        matcher = ToolMatcher(config)
        
        tool = matcher.find_link_tool('executable')
        
        assert tool is not None
        assert tool.action == 'link'
        assert tool.supports.get('output_type') == 'executable' or 'executable' in tool.name
    
    def test_ambiguous_tool_detection(self):
        """Test that ambiguous tool definitions are detected"""
        config_dict = {
            'name': 'test',
            'description': 'Test',
            'target_platform': 'linux',
            'target_architecture': 'x86_64',
            'execution_type': 'native',
            'tools': [
                {
                    'name': 'tool1',
                    'action': 'compile',
                    'input_extensions': ['.cpp'],
                    'output_extension': '.o',
                    'output_pattern': '{name}.o',
                    'command': 'tool1 {source}'
                },
                {
                    'name': 'tool2',
                    'action': 'compile',
                    'input_extensions': ['.cpp'],  # Same action + extension
                    'output_extension': '.obj',
                    'output_pattern': '{name}.obj',
                    'command': 'tool2 {source}'
                }
            ]
        }
        
        config = ToolchainConfig.from_dict(config_dict)
        
        # Should raise ValueError for ambiguous tools
        with pytest.raises(ValueError, match="Ambiguous"):
            matcher = ToolMatcher(config)
    
    def test_multiple_extensions_same_tool(self):
        """Test that a tool can handle multiple input extensions"""
        config_dict = {
            'name': 'test',
            'description': 'Test',
            'target_platform': 'linux',
            'target_architecture': 'x86_64',
            'execution_type': 'native',
            'tools': [
                {
                    'name': 'cpp_compile',
                    'action': 'compile',
                    'input_extensions': ['.cpp', '.cc', '.cxx'],
                    'output_extension': '.o',
                    'output_pattern': '{name}.o',
                    'command': 'g++ -c {source}'
                }
            ]
        }
        
        config = ToolchainConfig.from_dict(config_dict)
        matcher = ToolMatcher(config)
        
        tool_cpp = matcher.find_tool('compile', 'main.cpp')
        tool_cc = matcher.find_tool('compile', 'main.cc')
        tool_cxx = matcher.find_tool('compile', 'main.cxx')
        
        assert tool_cpp is not None
        assert tool_cc is not None
        assert tool_cxx is not None
        assert tool_cpp.name == tool_cc.name == tool_cxx.name


class TestToolchainManager:
    """Test ToolchainManager class"""
    
    def test_load_toolchain(self, temp_dir, create_toolchain_file, sample_toolchain_config):
        """Test loading a toolchain from file"""
        toolchain_file = create_toolchain_file('test-gcc', sample_toolchain_config)
        
        manager = ToolchainManager(str(temp_dir / 'toolchains'))
        toolchain = manager.get_toolchain('test-gcc')
        
        assert toolchain is not None
        assert toolchain.name == 'test-gcc'
        assert toolchain.description == 'Test GCC toolchain'
    
    def test_list_toolchains(self, temp_dir, create_toolchain_file, sample_toolchain_config):
        """Test listing available toolchains"""
        # Create first toolchain
        config1 = sample_toolchain_config.copy()
        config1['name'] = 'test-gcc'
        create_toolchain_file('test-gcc', config1)
        
        # Create second toolchain
        config2 = sample_toolchain_config.copy()
        config2['name'] = 'clang-linux'
        config2['description'] = 'Clang for Linux'
        create_toolchain_file('clang-linux', config2)
        
        manager = ToolchainManager(str(temp_dir / 'toolchains'))
        toolchains = manager.list_toolchains()
        
        assert len(toolchains) == 2
        names = [name for name, _ in toolchains]
        assert 'test-gcc' in names
        assert 'clang-linux' in names
    
    def test_auto_detect_toolchain(self, temp_dir, create_toolchain_file):
        """Test auto-detection of toolchain by platform/arch"""
        config = {
            'name': 'gcc-linux',
            'description': 'GCC for Linux',
            'target_platform': 'linux',
            'target_architecture': 'x86_64',
            'execution_type': 'native',
            'tools': []
        }
        create_toolchain_file('gcc-linux', config)
        
        manager = ToolchainManager(str(temp_dir / 'toolchains'))
        toolchain = manager.auto_detect('linux', 'x86_64')
        
        assert toolchain is not None
        assert toolchain.target_platform == 'linux'
        assert toolchain.target_architecture == 'x86_64'
    
    def test_get_nonexistent_toolchain(self, temp_dir):
        """Test that getting a nonexistent toolchain returns None"""
        manager = ToolchainManager(str(temp_dir / 'toolchains'))
        toolchain = manager.get_toolchain('nonexistent')
        
        assert toolchain is None
    
    def test_invalid_toolchain_file(self, temp_dir):
        """Test that invalid toolchain files are handled gracefully"""
        toolchain_dir = temp_dir / 'toolchains'
        toolchain_dir.mkdir(exist_ok=True)
        
        # Create invalid YAML file
        invalid_file = toolchain_dir / 'invalid.yaml'
        with open(invalid_file, 'w') as f:
            f.write("invalid: yaml: content: [")
        
        manager = ToolchainManager(str(toolchain_dir))
        toolchain = manager.get_toolchain('invalid')
        
        # Should return None for invalid file
        assert toolchain is None


class TestToolchainConfig:
    """Test ToolchainConfig class"""
    
    def test_from_dict(self, sample_toolchain_config):
        """Test creating ToolchainConfig from dictionary"""
        config = ToolchainConfig.from_dict(sample_toolchain_config)
        
        assert config.name == 'test-gcc'
        assert config.target_platform == 'linux'
        assert len(config.tools) == 2
        # tools is a dict, not a list - check first tool
        first_tool = list(config.tools.values())[0]
        assert isinstance(first_tool, Tool)
    
    def test_missing_required_fields(self):
        """Test that missing required fields raise errors"""
        incomplete_config = {
            # Missing 'name' field
            'tools': []
        }
        
        with pytest.raises((KeyError, TypeError)):
            ToolchainConfig.from_dict(incomplete_config)
    
    def test_tool_flags_by_configuration(self, sample_toolchain_config):
        """Test that tool flags vary by configuration"""
        config = ToolchainConfig.from_dict(sample_toolchain_config)
        # Get the cpp_compile tool
        compile_tool = config.tools['cpp_compile']
        
        assert 'debug' in compile_tool.flags
        assert 'release' in compile_tool.flags
        assert '-g' in compile_tool.flags['debug']
        assert '-O3' in compile_tool.flags['release']

