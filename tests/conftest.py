"""Pytest configuration and shared fixtures"""

import pytest
import tempfile
import shutil
from pathlib import Path
import yaml


@pytest.fixture
def temp_dir():
    """Create a temporary directory for test files"""
    tmpdir = tempfile.mkdtemp()
    yield Path(tmpdir)
    shutil.rmtree(tmpdir, ignore_errors=True)


@pytest.fixture
def sample_toolchain_config():
    """Sample toolchain configuration for testing"""
    return {
        'name': 'test-gcc',
        'description': 'Test GCC toolchain',
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
                'command': 'g++ -c {source} -o {output} {flags}',
                'flags': {
                    'debug': ['-g', '-O0'],
                    'release': ['-O3', '-DNDEBUG']
                }
            },
            {
                'name': 'link_executable',
                'action': 'link',
                'output_type': 'executable',
                'input_extensions': ['.o'],
                'output_extension': '',
                'output_pattern': '{name}',
                'command': 'g++ {objects} -o {output} {flags}',
                'flags': {
                    'debug': ['-g'],
                    'release': ['-O3']
                }
            }
        ]
    }


@pytest.fixture
def sample_build_template():
    """Sample build template for testing"""
    return {
        'executable': {
            'description': 'Compile sources and link into an executable',
            'steps': [
                {
                    'name': 'compile',
                    'action': 'compile',
                    'for_each': 'source in sources',
                    'output': '{output_dir}/obj/{source_stem}.{tool.output_ext}',
                    'tool_params': {
                        'defines': '{config.defines}',
                        'include_dirs': '{item.include_dirs}',
                        'extra_flags': '{config.compiler_flags}',
                        'std': '{config.cpp_standard}'
                    }
                },
                {
                    'name': 'link',
                    'action': 'link',
                    'output_type': 'executable',
                    'output': '{output_dir}/bin/{tool.output_pattern}',
                    'inputs': '{compile.outputs}',
                    'depends_on': '{compile.task_ids}'
                }
            ]
        }
    }


@pytest.fixture
def create_toolchain_file(temp_dir):
    """Factory fixture to create toolchain YAML files"""
    def _create(name: str, config: dict):
        toolchain_dir = temp_dir / 'toolchains'
        toolchain_dir.mkdir(exist_ok=True)
        toolchain_file = toolchain_dir / f'{name}.yaml'
        
        # Wrap in proper structure if not already wrapped
        if 'toolchain' not in config:
            wrapped_config = {
                'toolchain': {
                    'name': config['name'],
                    'description': config.get('description', ''),
                    'target': {
                        'platform': config.get('target_platform', 'linux'),
                        'architecture': config.get('target_architecture', 'x86_64')
                    },
                    'host': {
                        'platform': config.get('host_platform', 'linux'),
                        'architecture': config.get('host_architecture', 'x86_64')
                    },
                    'execution': {
                        'type': config.get('execution_type', 'native')
                    },
                    'tools': {}
                }
            }
            # Convert tools list to dict
            for tool in config.get('tools', []):
                tool_name = tool['name']
                wrapped_config['toolchain']['tools'][tool_name] = tool
            config = wrapped_config
        
        with open(toolchain_file, 'w') as f:
            yaml.dump(config, f)
        return toolchain_file
    return _create


@pytest.fixture
def create_source_file(temp_dir):
    """Factory fixture to create source files"""
    def _create(path: str, content: str):
        file_path = temp_dir / path
        file_path.parent.mkdir(parents=True, exist_ok=True)
        with open(file_path, 'w') as f:
            f.write(content)
        return file_path
    return _create

