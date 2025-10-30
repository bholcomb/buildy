"""Execution environments for running build commands"""

import os
import subprocess
import logging
from typing import Dict, Any

from .toolchain import ToolchainConfig
from .constants import DEFAULT_TASK_TIMEOUT_SECONDS

logger = logging.getLogger('buildy.execution')

class ExecutionEnvironment:
    """Base class for different execution environments (native, docker, etc.)"""
    
    @staticmethod
    def create(toolchain: ToolchainConfig) -> 'ExecutionEnvironment':
        """Factory method to create appropriate execution environment"""
        exec_type = toolchain.execution_type
        
        if exec_type == 'native':
            return NativeExecution()
        elif exec_type == 'docker':
            return DockerExecution(toolchain.execution_config)
        elif exec_type == 'wsl':
            return WSLExecution(toolchain.execution_config)
        else:
            logger.warning(f"Unknown execution type '{exec_type}', falling back to native")
            return NativeExecution()
    
    def execute(self, command: str, cwd: str = None, timeout: int = DEFAULT_TASK_TIMEOUT_SECONDS) -> subprocess.CompletedProcess:
        """Execute command in the environment
        
        Args:
            command: Command to execute
            cwd: Working directory
            timeout: Timeout in seconds
            
        Returns:
            CompletedProcess result
        """
        raise NotImplementedError

class NativeExecution(ExecutionEnvironment):
    """Execute commands natively on the host system"""
    
    def execute(self, command: str, cwd: str = None, timeout: int = DEFAULT_TASK_TIMEOUT_SECONDS) -> subprocess.CompletedProcess:
        """Execute command directly on host"""
        return subprocess.run(
            command,
            shell=True,
            capture_output=True,
            text=True,
            timeout=timeout,
            cwd=cwd or os.getcwd()
        )

class DockerExecution(ExecutionEnvironment):
    """Execute commands inside Docker container"""
    
    def __init__(self, config: Dict[str, Any]):
        self.image = config.get('image', 'gcc:13')
        self.volumes = config.get('volumes', [])
        self.working_dir = config.get('working_dir', '/workspace')
        self.user = config.get('user', f"{os.getuid()}:{os.getgid()}")
    
    def execute(self, command: str, cwd: str = None, timeout: int = DEFAULT_TASK_TIMEOUT_SECONDS) -> subprocess.CompletedProcess:
        """Execute command inside Docker container"""
        # Expand environment variables in volume mounts
        expanded_volumes = []
        for vol in self.volumes:
            # Replace ${PWD} with current directory
            expanded = vol.replace('${PWD}', os.getcwd())
            # Replace ${UID} and ${GID}
            expanded = expanded.replace('${UID}', str(os.getuid()))
            expanded = expanded.replace('${GID}', str(os.getgid()))
            expanded_volumes.append(expanded)
        
        # Expand user string
        user = self.user.replace('${UID}', str(os.getuid())).replace('${GID}', str(os.getgid()))
        
        # Build docker run command
        volume_args = ' '.join(f"-v {v}" for v in expanded_volumes)
        
        # Escape single quotes in command for shell
        escaped_command = command.replace("'", "'\"'\"'")
        
        docker_cmd = (
            f"docker run --rm "
            f"{volume_args} "
            f"-w {self.working_dir} "
            f"-u {user} "
            f"{self.image} "
            f"sh -c '{escaped_command}'"
        )
        
        logger.debug(f"Docker command: {docker_cmd}")
        
        return subprocess.run(
            docker_cmd,
            shell=True,
            capture_output=True,
            text=True,
            timeout=timeout,
            cwd=cwd or os.getcwd()
        )

class WSLExecution(ExecutionEnvironment):
    """Execute commands inside Windows Subsystem for Linux"""
    
    def __init__(self, config: Dict[str, Any]):
        self.distribution = config.get('distribution', 'Ubuntu')
        self.user = config.get('user', None)
    
    def execute(self, command: str, cwd: str = None, timeout: int = DEFAULT_TASK_TIMEOUT_SECONDS) -> subprocess.CompletedProcess:
        """Execute command inside WSL"""
        # Build wsl command
        wsl_cmd = f"wsl -d {self.distribution}"
        
        if self.user:
            wsl_cmd += f" -u {self.user}"
        
        # Escape command for WSL
        escaped_command = command.replace('"', '\\"')
        wsl_cmd += f' -- bash -c "{escaped_command}"'
        
        logger.debug(f"WSL command: {wsl_cmd}")
        
        return subprocess.run(
            wsl_cmd,
            shell=True,
            capture_output=True,
            text=True,
            timeout=timeout,
            cwd=cwd or os.getcwd()
        )

