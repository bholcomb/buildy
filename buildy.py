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

# Get the directory where buildy.py is located
BUILDY_DIR = Path(__file__).parent.resolve()

# Import from buildy_lib package
from buildy_lib import (
    BuildCache,
    TaskGraph,
    ConfigParser,
    TaskExecutor,
    ToolchainManager,
    IncrementalBuilder,
    Workspace
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
    parser.add_argument('config_files', nargs='*', help='Build configuration files (or omit for workspace mode)')
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
    parser.add_argument('--toolchains-dir', default=str(BUILDY_DIR / 'data' / 'toolchains'),
                       help='Directory containing toolchain configurations')
    parser.add_argument('--templates-file', default=str(BUILDY_DIR / 'data' / 'templates' / 'buildy_templates.yaml'),
                       help='Path to build templates file')
    parser.add_argument('--force', action='store_true',
                       help='Force full rebuild, ignore cache and build state')
    parser.add_argument('--target', action='append', dest='targets', metavar='NAME',
                       help='Build specific target(s) (workspace mode only, can be used multiple times)')
    parser.add_argument('--all', action='store_true', dest='build_all',
                       help='Build all targets in workspace (default if no --target specified)')

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
        
        # Initialize template engine with correct path
        from buildy_lib import BuildTemplateEngine
        template_engine = BuildTemplateEngine(args.templates_file)
        
        config_parser = ConfigParser(
            args.platform, 
            args.architecture, 
            args.configuration, 
            cli_defines,
            toolchain_manager,
            args.toolchain,
            template_engine=template_engine
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
        
        # Determine build mode: workspace or single-file
        workspace = None
        config_file = None
        
        if not args.config_files:
            # No config files specified - try workspace mode
            logger.info("No config file specified, attempting workspace discovery...")
            
            # Try loading from cache first
            workspace = Workspace.load_discovery_cache(Path(args.cache_dir))
            if workspace:
                logger.info(f"Loaded workspace from cache: {workspace.root_dir}")
            else:
                # Cache miss or invalid, do full discovery
                workspace = Workspace.discover(os.getcwd())
                if not workspace:
                    logger.error("No workspace found. Either provide a config file or run from a directory with buildy.yaml")
                    parser.print_help()
                    return 1
                logger.info(f"Discovered workspace at: {workspace.root_dir}")
            
            workspace.discover_modules()
            logger.info(f"Found {len(workspace.modules)} module(s)")
        else:
            # Config file(s) specified - could be a file or directory
            config_file = args.config_files[0]
            if not os.path.exists(config_file):
                logger.error(f"Configuration file not found: {config_file}")
                return 1
            
            # If it's a directory, look for buildy.yaml in it
            if os.path.isdir(config_file):
                config_file = os.path.join(config_file, 'buildy.yaml')
                if not os.path.exists(config_file):
                    logger.error(f"No buildy.yaml found in directory: {args.config_files[0]}")
                    return 1
            
            # Check if this is a workspace root (has modules defined)
            config_path = Path(config_file).resolve()
            if config_path.name == 'buildy.yaml':
                # Try to load as workspace
                try:
                    workspace = Workspace(config_path.parent)
                    workspace.discover_modules()
                    if workspace.modules:
                        logger.info(f"Loaded workspace from: {workspace.root_dir}")
                        logger.info(f"Found {len(workspace.modules)} module(s)")
                    else:
                        # No modules, treat as single file
                        workspace = None
                except Exception as e:
                    logger.debug(f"Not a workspace: {e}")
                    workspace = None
            
            # Warn about multiple config files (not yet supported)
            if len(args.config_files) > 1:
                logger.warning("Multiple config files not yet supported, using first one")
        
        # Validate target/all flags
        if args.targets and not workspace:
            logger.error("--target flag requires workspace mode")
            return 1
        if args.build_all and not workspace:
            logger.error("--all flag requires workspace mode")
            return 1
        
        # Build using appropriate mode
        if workspace:
            # Workspace mode
            target_filter = args.targets if args.targets else None
            
            # Create workspace-aware config parser
            config_parser = ConfigParser(
                args.platform,
                args.architecture,
                args.configuration,
                cli_defines,
                toolchain_manager,
                args.toolchain,
                template_engine=template_engine,
                workspace=workspace
            )
            
            # Set cache directory relative to workspace root
            cache_dir = workspace.root_dir / args.cache_dir
            builder = IncrementalBuilder(str(cache_dir), args.workers)
            success = builder.build_workspace(
                workspace,
                config_parser,
                toolchain_manager,
                config_parser.template_engine,
                target_filter=target_filter,
                dry_run=args.dry_run,
                force=args.force
            )
        else:
            # Single-file mode
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
