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
    ToolchainManager,
    IncrementalBuilder
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
    parser.add_argument('--force', action='store_true',
                       help='Force full rebuild, ignore cache and build state')

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

        # Always use incremental builder (it handles both incremental and full builds)
        state_file = Path(args.cache_dir) / "build_state.json"
        
        # For now, only support single config file with incremental builds
        if len(args.config_files) > 1:
            logger.warning("Multiple config files not yet supported with incremental builds, using first one")
        
        config_file = args.config_files[0]
        if not os.path.exists(config_file):
            logger.error(f"Configuration file not found: {config_file}")
            return 1
        
        builder = IncrementalBuilder(args.cache_dir, args.workers)
        success = builder.build(
            config_file,
            config_parser,
            toolchain_manager,
            config_parser.template_engine,
            config_parser.var_env,
            dry_run=args.dry_run,
            force=args.force
        )
        
        return 0 if success else 1

    except KeyboardInterrupt:
        logger.warning("\nBuild interrupted by user")
        return 130
    except Exception as e:
        logger.error(f"Unexpected error: {e}", exc_info=True)
        return 1

if __name__ == '__main__':
    sys.exit(main())
