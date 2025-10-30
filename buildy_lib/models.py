"""Data models for build tasks and resources"""

import os
import hashlib
import json
from dataclasses import dataclass
from typing import List

@dataclass
class ResourceRequirements:
    """Resource requirements for a task"""
    cpu_cores: int = 1
    memory_mb: int = 100
    disk_mb: int = 10

@dataclass
class TaskInput:
    """Input file with content hash"""
    path: str
    hash: str = ""

    def __post_init__(self):
        if not self.hash and os.path.exists(self.path):
            self.hash = self._calculate_hash()

    def _calculate_hash(self) -> str:
        """Calculate SHA-256 hash of file content"""
        try:
            with open(self.path, 'rb') as f:
                return hashlib.sha256(f.read()).hexdigest()
        except (OSError, IOError):
            return "missing_file"

@dataclass
class BuildTask:
    """Individual build task following Blizzard's parse/execute model"""
    task_id: str
    task_type: str
    inputs: List[TaskInput]
    outputs: List[str]
    dependencies: List[str]
    command: str
    platform: str = "linux"
    architecture: str = "x86_64"
    configuration: str = "debug"
    estimated_time: float = 1.0
    resource_requirements: ResourceRequirements = None
    cache_key: str = ""

    def __post_init__(self):
        if self.resource_requirements is None:
            self.resource_requirements = ResourceRequirements()
        if not self.cache_key:
            self.cache_key = self._calculate_cache_key()

    def _calculate_cache_key(self) -> str:
        """Calculate content-addressable cache key"""
        content = {
            'task_type': self.task_type,
            'inputs': [(inp.path, inp.hash) for inp in self.inputs],
            'command': self.command,
            'platform': self.platform,
            'architecture': self.architecture,
            'configuration': self.configuration
        }
        return hashlib.sha256(json.dumps(content, sort_keys=True).encode()).hexdigest()

