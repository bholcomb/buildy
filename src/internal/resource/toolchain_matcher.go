package resource

import (
	"path/filepath"

	"buildy/pkg/util"
)

// ToolMatcher finds a toolchain's tool for an action and a file. The toolchain loader has
// rejected ambiguous toolchains (two tools for one action and extension, two link tools for one
// output type, two build tools), so every lookup has at most one answer.
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
	for _, name := range toolchain.Tools.Keys {
		tool := toolchain.Tools.Values[name]
		if tool.Action == "link" || tool.Action == "build" {
			continue
		}
		for _, ext := range tool.InputExtensions {
			tm.toolMap[tool.Action+":"+ext] = tool
		}
	}
	return tm, nil
}

// FindTool finds a tool that matches the action and file extension
func (tm *ToolMatcher) FindTool(action, filePath string) *Tool {
	ext := filepath.Ext(filePath)
	if ext == "" {
		return nil
	}
	tool := tm.toolMap[action+":"+ext]
	if tool != nil {
		util.LogInfo("Matched %s (%s) -> tool '%s'", filePath, action, tool.Name)
	} else {
		util.LogInfo("No tool found for action='%s' extension='%s'", action, ext)
	}
	return tool
}

// FindBuildTool finds the toolchain's build tool (a single step that compiles and links, like
// go build)
func (tm *ToolMatcher) FindBuildTool() *Tool {
	if tools := tm.toolchain.ToolsWithAction("build"); len(tools) > 0 {
		return tools[0]
	}
	return nil
}

// FindLinkTool finds the link tool for an output type (supports.output_type)
func (tm *ToolMatcher) FindLinkTool(outputType string) *Tool {
	for _, tool := range tm.toolchain.ToolsWithAction("link") {
		if tool.Supports.OutputType == outputType {
			return tool
		}
	}
	return nil
}

// Execution returns where the toolchain's commands run.
func (tm *ToolMatcher) Execution() util.ExecSpec {
	return tm.toolchain.Execution
}

// GetOutputPrefix returns the toolchain's default output prefix for library filenames
func (tm *ToolMatcher) GetOutputPrefix() string {
	return tm.toolchain.OutputPrefix
}
