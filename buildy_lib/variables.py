"""Variable environment with hierarchical scoping and provenance tracking"""

import logging
from typing import Dict, List, Optional, Any

logger = logging.getLogger('buildy.variables')

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

