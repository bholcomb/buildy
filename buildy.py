#!/usr/bin/env python3
"""
Buildy - A prototype task-based build system
Based on the ideal C++ build system design with Blizzard-inspired architecture
"""

import os
import sys
import json
import time
import argparse
import logging
from pathlib import Path
from dataclasses import asdict

# Import from buildy_lib package
from buildy_lib import (
    BuildCache,
    TaskGraph,
    ConfigParser,
    TaskExecutor,
    ToolchainManager
)
from buildy_lib.constants import DEFAULT_MAX_WORKERS

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger('buildy')

def main():
    """Main entry point"""
    parser = argparse.ArgumentParser(description='Buildy - Task-based build system prototype')
    parser.add_argument('config_files', nargs='*', help='Build configuration files')
    parser.add_argument('--platform', default='linux', help='Target platform')
    parser.add_argument('--architecture', default='x86_64', help='Target architecture') 
    parser.add_argument('--configuration', default='debug', help='Build configuration')
    parser.add_argument('--cache-dir', default='.buildy_cache', help='Cache directory')
    parser.add_argument('--dry-run', action='store_true', help='Generate tasks but don\'t execute')
    parser.add_argument('--workers', type=int, default=DEFAULT_MAX_WORKERS, help='Max parallel workers')
    parser.add_argument('--cache-stats', action='store_true', help='Show cache statistics')
    parser.add_argument('--verbose', '-v', action='store_true', help='Enable verbose logging')
    parser.add_argument('--define', '-D', action='append', dest='defines', metavar='VAR=VALUE',
                       help='Define a variable (can be used multiple times, e.g. -D MY_VAR=value)')
    parser.add_argument('--toolchain', '-t', dest='toolchain', metavar='NAME',
                       help='Specify toolchain to use (overrides config file)')
    parser.add_argument('--list-toolchains', action='store_true',
                       help='List available toolchains and exit')
    parser.add_argument('--toolchains-dir', default='toolchains',
                       help='Directory containing toolchain configurations')

    args = parser.parse_args()

    # Configure logging level
    if args.verbose:
        logging.getLogger('buildy').setLevel(logging.DEBUG)
    
    # Parse CLI defines
    cli_defines = {}
    if args.defines:
        for define in args.defines:
            if '=' not in define:
                logger.error(f"Invalid --define format: '{define}' (expected VAR=VALUE)")
                return 1
            var, value = define.split('=', 1)
            cli_defines[var.strip()] = value.strip()
            logger.debug(f"CLI define: {var.strip()}={value.strip()}")

    try:
        # Initialize toolchain manager
        toolchain_manager = ToolchainManager(args.toolchains_dir)
        
        # Handle --list-toolchains
        if args.list_toolchains:
            logger.info("Available toolchains:")
            for name, description in toolchain_manager.list_toolchains():
                logger.info(f"  {name:20s} - {description}")
            return 0
        
        # Initialize components
        cache = BuildCache(args.cache_dir)
        config_parser = ConfigParser(
            args.platform, 
            args.architecture, 
            args.configuration, 
            cli_defines,
            toolchain_manager,
            args.toolchain
        )
        graph = TaskGraph()

        # Show cache stats if requested
        if args.cache_stats:
            stats = cache.get_cache_stats()
            logger.info(f"Cache Statistics:")
            logger.info(f"  Total entries: {stats['total_entries']}")
            logger.info(f"  Total size: {stats['total_size_mb']:.1f} MB")
            logger.info(f"  Cache directory: {stats['cache_directory']}")
            return 0
        
        # Ensure config files are provided
        if not args.config_files:
            logger.error("No configuration files provided")
            parser.print_help()
            return 1

        # Parse configuration files and generate tasks
        all_tasks = []
        for config_file in args.config_files:
            if not os.path.exists(config_file):
                logger.error(f"Configuration file not found: {config_file}")
                return 1
            
            logger.info(f"Parsing {config_file}...")
            config = config_parser.parse_config_file(config_file)
            if not config:
                logger.error(f"Failed to parse configuration file: {config_file}")
                return 1
            
            tasks = config_parser.generate_tasks(config)
            all_tasks.extend(tasks)

        if not all_tasks:
            logger.error("No tasks generated from configuration files")
            return 1

        # Build task graph
        for task in all_tasks:
            graph.add_task(task)

        try:
            graph.build_execution_stages()
        except ValueError as e:
            logger.error(f"Error building task graph: {e}")
            return 1

        # Always output task graph to cache directory
        try:
            # Get toolchain info
            toolchain_info = {
                'name': config_parser.current_toolchain.name,
                'description': config_parser.current_toolchain.description,
                'target_platform': config_parser.current_toolchain.target_platform,
                'target_architecture': config_parser.current_toolchain.target_architecture,
                'execution_type': config_parser.current_toolchain.execution_type
            }
            
            output_data = {
                'metadata': {
                    'platform': args.platform,
                    'architecture': args.architecture,
                    'configuration': args.configuration,
                    'generated_at': time.time(),
                    'total_tasks': len(all_tasks),
                    'toolchain': toolchain_info
                },
                'resolved_variables': config_parser.var_env.get_all_variables(),
                'tasks': [asdict(task) for task in all_tasks],
                'execution_plan': graph.get_execution_plan()
            }

            # Always save to cache directory
            output_path = cache.cache_dir / "tasks.json"
            
            with open(output_path, 'w') as f:
                json.dump(output_data, f, indent=2, default=str)

            logger.debug(f"Task graph saved to {output_path}")
        except (OSError, IOError) as e:
            logger.warning(f"Failed to write task graph: {e}")
            # Don't fail the build if we can't write the task graph

        # Execute tasks with execution environment from config parser
        executor = TaskExecutor(cache, args.workers, exec_env=config_parser.exec_env)
        success = executor.execute_task_graph(graph, args.dry_run)

        return 0 if success else 1

    except KeyboardInterrupt:
        logger.warning("\nBuild interrupted by user")
        return 130
    except Exception as e:
        logger.error(f"Unexpected error: {e}", exc_info=True)
        return 1

if __name__ == '__main__':
    sys.exit(main())
