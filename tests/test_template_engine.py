"""Tests for BuildTemplateEngine class"""

import pytest
from pathlib import Path
from buildy_lib.template_engine import BuildTemplateEngine
from buildy_lib.toolchain import ToolchainConfig, ToolMatcher, CommandBuilder
from buildy_lib.models import BuildTask


class TestBuildTemplateEngine:
    """Test build template expansion"""
    
    def test_load_templates(self, temp_dir, sample_build_template):
        """Test loading templates from YAML file"""
        import yaml
        
        template_file = temp_dir / 'templates.yaml'
        with open(template_file, 'w') as f:
            yaml.dump({'templates': sample_build_template}, f)
        
        engine = BuildTemplateEngine(str(template_file))
        
        assert 'executable' in engine.templates
        assert engine.templates['executable']['description'] == 'Compile sources and link into an executable'
    
    def test_get_template(self, temp_dir, sample_build_template):
        """Test retrieving a template by name"""
        import yaml
        
        template_file = temp_dir / 'templates.yaml'
        with open(template_file, 'w') as f:
            yaml.dump({'templates': sample_build_template}, f)
        
        engine = BuildTemplateEngine(str(template_file))
        template = engine.get_template('executable')
        
        assert template is not None
        assert 'steps' in template
        assert len(template['steps']) == 2
    
    def test_list_templates(self, temp_dir, sample_build_template):
        """Test listing available templates"""
        import yaml
        
        template_file = temp_dir / 'templates.yaml'
        with open(template_file, 'w') as f:
            yaml.dump({'templates': sample_build_template}, f)
        
        engine = BuildTemplateEngine(str(template_file))
        templates = engine.list_templates()
        
        assert len(templates) == 1
        assert templates[0][0] == 'executable'
        assert 'Compile sources' in templates[0][1]
    
    def test_resolve_template_string_simple(self):
        """Test simple template string resolution"""
        engine = BuildTemplateEngine()
        
        context = {
            'platform': 'linux',
            'arch': 'x86_64',
            'output_dir': 'build'
        }
        
        result = engine._resolve_template_string('{output_dir}/{platform}-{arch}', context)
        
        assert result == 'build/linux-x86_64'
    
    def test_resolve_template_string_nested(self):
        """Test nested object resolution"""
        engine = BuildTemplateEngine()
        
        context = {
            'config': {
                'defines': ['DEBUG', 'VERSION=1.0']
            },
            'tool': {
                'output_ext': '.o'
            }
        }
        
        result = engine._resolve_template_string('{tool.output_ext}', context)
        assert result == '.o'
    
    def test_resolve_template_string_preserves_lists(self):
        """Test that list references are preserved as lists"""
        engine = BuildTemplateEngine()
        
        context = {
            'config': {
                'defines': ['DEBUG', 'VERSION=1.0'],
                'flags': ['-Wall', '-Wextra']
            }
        }
        
        # Direct reference to list should return the list
        result = engine._resolve_template_string('{config.defines}', context)
        assert isinstance(result, list)
        assert result == ['DEBUG', 'VERSION=1.0']
    
    def test_resolve_reference(self):
        """Test resolving references to previous step results"""
        engine = BuildTemplateEngine()
        
        step_results = {
            'compile': {
                'outputs': ['file1.o', 'file2.o'],
                'task_ids': ['compile_001', 'compile_002']
            }
        }
        
        outputs = engine._resolve_reference('{compile.outputs}', step_results)
        task_ids = engine._resolve_reference('{compile.task_ids}', step_results)
        
        assert outputs == ['file1.o', 'file2.o']
        assert task_ids == ['compile_001', 'compile_002']
    
    def test_resolve_tool_params(self):
        """Test resolving tool parameters"""
        engine = BuildTemplateEngine()
        
        context = {
            'config': {
                'defines': ['DEBUG'],
                'cpp_standard': 'c++20'
            },
            'item': {
                'include_dirs': ['include/', 'src/']
            }
        }
        
        params = {
            'defines': '{config.defines}',
            'std': '{config.cpp_standard}',
            'include_dirs': '{item.include_dirs}'
        }
        
        resolved = engine._resolve_tool_params(params, context)
        
        assert resolved['defines'] == ['DEBUG']
        assert resolved['std'] == 'c++20'
        assert resolved['include_dirs'] == ['include/', 'src/']
    
    def test_expand_foreach_step(self, temp_dir, create_source_file, sample_toolchain_config):
        """Test expanding a for_each step into multiple tasks"""
        engine = BuildTemplateEngine()
        
        # Create test source files
        src1 = create_source_file('src/file1.cpp', 'int main() {}')
        src2 = create_source_file('src/file2.cpp', 'void func() {}')
        
        config = ToolchainConfig.from_dict(sample_toolchain_config)
        matcher = ToolMatcher(config)
        builder = CommandBuilder(config, matcher, 'debug')
        
        step = {
            'name': 'compile',
            'action': 'compile',
            'for_each': 'source in sources',
            'output': '{output_dir}/obj/{source_stem}.{tool.output_ext}',
            'tool_params': {
                'defines': '{config.defines}',
                'std': 'c++20'
            }
        }
        
        item_config = {
            'name': 'test_exe',
            'sources': [str(src1), str(src2)]
        }
        
        merged_config = {
            'defines': ['DEBUG'],
            'cpp_standard': 'c++20'
        }
        
        context = {
            'item': item_config,
            'config': merged_config,
            'output_dir': 'build'
        }
        
        tasks = engine._expand_foreach_step(
            step, context, item_config, merged_config,
            'build', 'setup_001', 1,
            matcher, builder, 'linux', 'x86_64', 'debug'
        )
        
        assert len(tasks) == 2
        assert all(isinstance(task, BuildTask) for task in tasks)
        assert tasks[0].task_type == 'compile'
        assert tasks[1].task_type == 'compile'
    
    def test_expand_single_step(self, sample_toolchain_config):
        """Test expanding a single (non-foreach) step"""
        engine = BuildTemplateEngine()
        
        config = ToolchainConfig.from_dict(sample_toolchain_config)
        matcher = ToolMatcher(config)
        builder = CommandBuilder(config, matcher, 'debug')
        
        step = {
            'name': 'link',
            'action': 'link',
            'output_type': 'executable',
            'output': '{output_dir}/bin/{tool.output_pattern}',
            'inputs': '{compile.outputs}',
            'depends_on': ['{compile.task_ids}']
        }
        
        step_results = {
            'compile': {
                'outputs': ['build/obj/file1.o', 'build/obj/file2.o'],
                'task_ids': ['compile_001', 'compile_002']
            }
        }
        
        item_config = {
            'name': 'test_exe'
        }
        
        merged_config = {}
        
        context = {
            'item': item_config,
            'config': merged_config,
            'output_dir': 'build'
        }
        
        task = engine._expand_single_step(
            step, context, step_results, item_config, merged_config,
            'build', 1, matcher, builder, 'linux', 'x86_64', 'debug', []
        )
        
        assert task is not None
        assert isinstance(task, BuildTask)
        assert task.task_type == 'link'
        assert len(task.inputs) == 2
        assert 'compile_001' in task.dependencies
        assert 'compile_002' in task.dependencies
    
    def test_missing_template_file(self, temp_dir):
        """Test handling of missing template file"""
        engine = BuildTemplateEngine(str(temp_dir / 'nonexistent.yaml'))
        
        # Should not crash, just return empty templates
        assert len(engine.templates) == 0
    
    def test_invalid_template_reference(self):
        """Test handling of invalid template reference"""
        engine = BuildTemplateEngine()
        
        with pytest.raises(ValueError, match="Template .* not found"):
            engine.expand_template(
                'nonexistent_template',
                {}, {}, 'build', 'setup_001', 1,
                None, None, 'linux', 'x86_64', 'debug'
            )

