package resource

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"buildy/pkg/util"
)

// ToolMatcher matches files to appropriate tools based on action and extension
type ToolMatcher struct {
	toolchain *ToolchainConfig
	toolMap   map[string]*Tool // key: "action:extension"
}

// NewToolMatcher creates a new ToolMatcher
func NewToolMatcher(toolchain *ToolchainConfig) (*ToolMatcher, error) {
	tm := &ToolMatcher{
		toolchain: toolchain,
		toolMap:   make(map[string]*Tool),
	}

	if err := tm.buildToolMap(); err != nil {
		return nil, err
	}

	return tm, nil
}

// buildToolMap builds the action+extension lookup map
func (tm *ToolMatcher) buildToolMap() error {
	for _, tool := range tm.toolchain.Tools {
		// Only build map for non-link actions
		// Link tools are handled separately by FindLinkTool()
		if tool.Action != "link" {
			for _, ext := range tool.InputExtensions {
				key := tool.Action + ":" + ext
				if existing, exists := tm.toolMap[key]; exists {
					return fmt.Errorf(
						"ambiguous tool definition in toolchain '%s': "+
							"both '%s' and '%s' handle action='%s' with extension='%s'",
						tm.toolchain.Name, existing.Name, tool.Name, tool.Action, ext,
					)
				}
				tm.toolMap[key] = tool
			}
		}
	}
	return nil
}

// FindTool finds a tool that matches the action and file extension
func (tm *ToolMatcher) FindTool(action, filePath string) *Tool {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return nil
	}

	key := action + ":" + ext
	tool := tm.toolMap[key]

	if tool != nil {
		log.Printf("Matched %s (%s) -> tool '%s'", filePath, action, tool.Name)
	} else {
		log.Printf("No tool found for action='%s' extension='%s'", action, ext)
	}

	return tool
}

// FindBuildTool finds a tool for building (single-step compile+link like Go)
func (tm *ToolMatcher) FindBuildTool() *Tool {
	// Look for tool with action='build'
	for _, tool := range tm.toolchain.Tools {
		if tool.Action == "build" {
			log.Printf("Matched build tool -> '%s'", tool.Name)
			return tool
		}
	}

	return nil
}

// FindLinkTool finds a tool for linking based on output type
func (tm *ToolMatcher) FindLinkTool(outputType string) *Tool {
	// Look for tool with action='link' and matching output type
	for _, tool := range tm.toolchain.Tools {
		if tool.Action == "link" {
			// Check if tool name or supports indicates it handles this output type
			if strings.Contains(tool.Name, outputType) {
				log.Printf("Matched link tool for '%s' -> '%s'", outputType, tool.Name)
				return tool
			}
			if supportedType, ok := tool.Supports["output_type"].(string); ok && supportedType == outputType {
				log.Printf("Matched link tool for '%s' -> '%s'", outputType, tool.Name)
				return tool
			}
		}
	}

	util.BuildWarning("toolchain", "No link tool found for output_type='%s'", outputType)
	return nil
}
