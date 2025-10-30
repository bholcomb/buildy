"""Variable environment with hierarchical scoping and provenance tracking"""

import logging
from typing import Dict, List, Optional, Any

logger = logging.getLogger('buildy.variables')

class VariableEnvironment:
    """Hierarchical variable environment with provenance tracking and chaining"""
    
    def __init__(self, parent: Optional['VariableEnvironment'] = None):
        """
        Initialize variable environment.
        
        Args:
            parent: Parent environment to chain to (for inheritance)
        """
        # Variables stored with their source for provenance
        self.variables: Dict[str, tuple[str, str]] = {}  # name -> (value, source)
        self.scopes: List[str] = []  # Stack of scope names for tracking
        self.parent = parent  # Parent environment for chaining
    
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
    
    def get_variable(self, name: str) -> Optional[str]:
        """
        Get a variable value, searching parent chain if not found locally.
        
        Args:
            name: Variable name
            
        Returns:
            Variable value or None if not found
        """
        result = self.variables.get(name)
        if result:
            return result[0]
        
        # Search parent environment if not found locally
        if self.parent:
            return self.parent.get_variable(name)
        
        return None
    
    def get_provenance(self, name: str) -> Optional[str]:
        """
        Get the source/provenance of a variable, searching parent chain.
        
        Args:
            name: Variable name
            
        Returns:
            Source/provenance or None if not found
        """
        result = self.variables.get(name)
        if result:
            return result[1]
        
        # Search parent environment if not found locally
        if self.parent:
            return self.parent.get_provenance(name)
        
        return None
    
    def get_variable_with_source(self, name: str) -> Optional[tuple[str, str]]:
        """
        Get a variable and its source, searching parent chain.
        
        Args:
            name: Variable name
            
        Returns:
            Tuple of (value, source) or None if not found
        """
        result = self.variables.get(name)
        if result:
            return result
        
        # Search parent environment if not found locally
        if self.parent:
            return self.parent.get_variable_with_source(name)
        
        return None
    
    def resolve_string(self, text: str, collected_errors: List[str] = None, max_iterations: int = 10) -> str:
        """Resolve all ${variable} references in a string
        
        Args:
            text: String to resolve
            collected_errors: List to collect unresolved variable errors
            max_iterations: Maximum number of resolution passes (prevents infinite loops)
            
        Returns:
            Resolved string
        """
        if not isinstance(text, str):
            return text
        
        import re
        
        # Find all ${variable} patterns
        pattern = r'\$\{([^}]+)\}'
        
        # Resolve iteratively to handle nested variables
        for iteration in range(max_iterations):
            unresolved = []
            
            def replace_var(match):
                var_name = match.group(1)
                value = self.get_variable(var_name)
                if value is None:
                    unresolved.append(var_name)
                    return match.group(0)  # Keep original if not found
                return value
            
            new_text = re.sub(pattern, replace_var, text)
            
            # If nothing changed, we're done
            if new_text == text:
                break
            
            text = new_text
            
            # If we still have unresolved variables and nothing changed, stop
            if unresolved:
                break
        
        # Collect errors if requested
        if unresolved and collected_errors is not None:
            for var in unresolved:
                collected_errors.append(f"Unresolved variable '${{${var}}}' in: {text}")
        
        return text
    
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
    
    def get_all_variables(self, include_parent: bool = True) -> Dict[str, Dict[str, str]]:
        """
        Get all variables with their values and sources for export.
        
        Args:
            include_parent: Include variables from parent environment
            
        Returns:
            Dictionary of variable_name -> {value, source}
        """
        result = {}
        
        # Get parent variables first (so local overrides them)
        if include_parent and self.parent:
            result.update(self.parent.get_all_variables(include_parent=True))
        
        # Add local variables (overriding parent)
        for name, (value, source) in self.variables.items():
            result[name] = {"value": value, "source": source}
        
        return result
    
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

