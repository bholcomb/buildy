"""
Persistent build state management for incremental builds.
"""

import json
import hashlib
from dataclasses import dataclass, asdict
from pathlib import Path
from typing import Dict, Optional, Any
import logging

logger = logging.getLogger(__name__)


@dataclass
class TaskResult:
    """Result of a completed task"""
    task_id: str
    cache_key: str
    success: bool
    outputs: list[str]
    timestamp: float


@dataclass
class BuildState:
    """Persistent build state across invocations"""
    last_build_time: float
    task_graph_hash: str  # Hash of the config + toolchain
    completed_tasks: Dict[str, Dict[str, Any]]  # task_id -> result dict
    file_mtimes: Dict[str, float]  # file_path -> modification time
    config_hash: str  # Hash of build configuration
    toolchain_hash: str = ""  # Hash of toolchain configuration
    
    def save(self, state_file: Path):
        """Save build state to disk"""
        try:
            state_file.parent.mkdir(parents=True, exist_ok=True)
            with open(state_file, 'w') as f:
                json.dump(asdict(self), f, indent=2)
            logger.debug(f"Saved build state to {state_file}")
        except Exception as e:
            logger.warning(f"Failed to save build state: {e}")
    
    @staticmethod
    def load(state_file: Path) -> Optional['BuildState']:
        """Load build state from disk"""
        if not state_file.exists():
            logger.debug(f"No build state found at {state_file}")
            return None
        
        try:
            with open(state_file, 'r') as f:
                data = json.load(f)
            
            # Handle missing fields for backward compatibility
            if 'toolchain_hash' not in data:
                data['toolchain_hash'] = ""
            
            logger.debug(f"Loaded build state from {state_file}")
            return BuildState(**data)
        except Exception as e:
            logger.warning(f"Failed to load build state: {e}")
            return None
    
    @staticmethod
    def hash_config(config: Dict[str, Any]) -> str:
        """Generate hash of configuration"""
        # Convert config to stable JSON string
        config_str = json.dumps(config, sort_keys=True)
        return hashlib.sha256(config_str.encode()).hexdigest()
    
    @staticmethod
    def hash_file(file_path: str) -> str:
        """Generate hash of a file"""
        try:
            with open(file_path, 'rb') as f:
                return hashlib.sha256(f.read()).hexdigest()
        except Exception as e:
            logger.warning(f"Failed to hash file {file_path}: {e}")
            return ""

