"""Tests for VariableEnvironment class"""

import pytest
from buildy_lib.variables import VariableEnvironment


class TestVariableEnvironment:
    """Test variable resolution and provenance tracking"""
    
    def test_basic_variable_set_and_get(self):
        """Test basic variable setting and retrieval"""
        env = VariableEnvironment()
        env.set_variable("test_var", "test_value", "test_source")
        
        assert env.get_variable("test_var") == "test_value"
    
    def test_variable_not_found(self):
        """Test that missing variables return None"""
        env = VariableEnvironment()
        assert env.get_variable("nonexistent") is None
    
    def test_hierarchical_scopes(self):
        """Test that later scopes override earlier ones"""
        env = VariableEnvironment()
        
        env.push_scope("workspace")
        env.set_variable("version", "1.0", "workspace")
        
        env.push_scope("project")
        env.set_variable("version", "2.0", "project")
        
        # Project scope should override workspace
        assert env.get_variable("version") == "2.0"
    
    def test_provenance_tracking(self):
        """Test that provenance is correctly tracked"""
        env = VariableEnvironment()
        
        env.push_scope("workspace")
        env.set_variable("debug_level", "1", "workspace.config")
        
        env.push_scope("cli")
        env.set_variable("debug_level", "3", "cli.override")
        
        provenance = env.get_provenance("debug_level")
        assert provenance == "cli.override"
    
    def test_simple_string_resolution(self):
        """Test simple variable substitution in strings"""
        env = VariableEnvironment()
        env.set_variable("platform", "linux", "built-in")
        env.set_variable("arch", "x86_64", "built-in")
        
        errors = []
        result = env.resolve_string("build/${platform}-${arch}", errors)
        
        assert result == "build/linux-x86_64"
        assert len(errors) == 0
    
    def test_nested_variable_resolution(self):
        """Test nested variable references"""
        env = VariableEnvironment()
        env.set_variable("base", "build", "config")
        env.set_variable("platform", "linux", "built-in")
        env.set_variable("output", "${base}/${platform}", "config")
        
        errors = []
        result = env.resolve_string("${output}/bin", errors)
        
        assert result == "build/linux/bin"
        assert len(errors) == 0
    
    def test_unresolved_variable_error(self):
        """Test that unresolved variables are reported"""
        env = VariableEnvironment()
        env.set_variable("platform", "linux", "built-in")
        
        errors = []
        result = env.resolve_string("${platform}/${missing_var}", errors)
        
        assert len(errors) == 1
        assert "missing_var" in errors[0]
    
    def test_recursive_dict_resolution(self):
        """Test recursive resolution of nested dictionaries"""
        env = VariableEnvironment()
        env.set_variable("base_dir", "build", "config")
        env.set_variable("platform", "linux", "built-in")
        
        config = {
            "output": {
                "path": "${base_dir}/${platform}",
                "lib": "${base_dir}/${platform}/lib"
            },
            "version": "1.0"
        }
        
        errors = []
        resolved = env.resolve_recursive(config, errors)
        
        assert resolved["output"]["path"] == "build/linux"
        assert resolved["output"]["lib"] == "build/linux/lib"
        assert resolved["version"] == "1.0"
        assert len(errors) == 0
    
    def test_list_resolution(self):
        """Test resolution of variables in lists"""
        env = VariableEnvironment()
        env.set_variable("include_base", "/usr/include", "system")
        
        config = {
            "includes": [
                "${include_base}/c++",
                "${include_base}/python3.10"
            ]
        }
        
        errors = []
        resolved = env.resolve_recursive(config, errors)
        
        assert resolved["includes"][0] == "/usr/include/c++"
        assert resolved["includes"][1] == "/usr/include/python3.10"
        assert len(errors) == 0
    
    def test_extract_variables_from_section(self):
        """Test extracting variables from a config section"""
        env = VariableEnvironment()
        
        config = {
            "variables": {
                "compiler": "gcc",
                "version": "11"
            },
            "other_field": "value"
        }
        
        env.push_scope("test")
        env.extract_variables_from_section(config, "test")
        
        assert env.get_variable("compiler") == "gcc"
        assert env.get_variable("version") == "11"
        assert env.get_variable("other_field") is None
    
    def test_get_all_variables(self):
        """Test retrieving all variables with provenance"""
        env = VariableEnvironment()
        
        env.push_scope("workspace")
        env.set_variable("var1", "value1", "workspace")
        
        env.push_scope("project")
        env.set_variable("var2", "value2", "project")
        
        all_vars = env.get_all_variables()
        
        assert "var1" in all_vars
        assert "var2" in all_vars
        assert all_vars["var1"]["value"] == "value1"
        assert all_vars["var1"]["source"] == "workspace"
        assert all_vars["var2"]["value"] == "value2"
        assert all_vars["var2"]["source"] == "project"
    
    def test_circular_reference_detection(self):
        """Test that circular references are handled gracefully"""
        env = VariableEnvironment()
        env.set_variable("var_a", "${var_b}", "test")
        env.set_variable("var_b", "${var_a}", "test")
        
        errors = []
        # Should not hang, should report error
        result = env.resolve_string("${var_a}", errors)
        
        # After max iterations, should give up
        assert len(errors) > 0 or result.startswith("${")
    
    def test_empty_variable_value(self):
        """Test that empty strings are valid variable values"""
        env = VariableEnvironment()
        env.set_variable("empty", "", "test")
        
        errors = []
        result = env.resolve_string("prefix${empty}suffix", errors)
        
        assert result == "prefixsuffix"
        assert len(errors) == 0
    
    def test_special_characters_in_values(self):
        """Test that special characters in values are preserved"""
        env = VariableEnvironment()
        env.set_variable("flags", "-Wall -Wextra", "config")
        
        errors = []
        result = env.resolve_string("gcc ${flags} -o output", errors)
        
        assert result == "gcc -Wall -Wextra -o output"
        assert len(errors) == 0

