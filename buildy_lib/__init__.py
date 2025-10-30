"""
Buildy - A task-based build system with content-addressable caching
"""

__version__ = "0.1.0"

# Import main classes for easy access
from .models import BuildTask, TaskInput, ResourceRequirements
from .cache import BuildCache
from .task_graph import TaskGraph
from .toolchain import ToolchainConfig, Tool, ToolchainManager, ToolMatcher, CommandBuilder
from .execution import ExecutionEnvironment, NativeExecution, DockerExecution, WSLExecution
from .template_engine import BuildTemplateEngine
from .variables import VariableEnvironment
from .config_parser import ConfigParser
from .executor import TaskExecutor

__all__ = [
    'BuildTask',
    'TaskInput',
    'ResourceRequirements',
    'BuildCache',
    'TaskGraph',
    'ToolchainConfig',
    'Tool',
    'ToolchainManager',
    'ToolMatcher',
    'CommandBuilder',
    'ExecutionEnvironment',
    'NativeExecution',
    'DockerExecution',
    'WSLExecution',
    'BuildTemplateEngine',
    'VariableEnvironment',
    'ConfigParser',
    'TaskExecutor',
]

