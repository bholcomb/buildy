package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"buildy/internal/workspace"
)

func loadWorkspace(t *testing.T, root string, files map[string]string) *workspace.Workspace {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.NewWorkspace(root, workspace.DefaultCacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.DiscoverModules(cleanContext); err != nil {
		t.Fatal(err)
	}
	return ws
}

var cleanContext = workspace.BuildContext{Platform: "linux", Architecture: "x86_64", Configuration: "debug"}

// An output directory that holds the workspace's configs, or one outside the workspace root, is
// never removed; a cache directory outside the root isn't either.
func TestCleanRefusesWhatIsNotBuildys(t *testing.T) {
	root := t.TempDir()
	ws := loadWorkspace(t, root, map[string]string{
		"buildy.yaml":     "project: {name: c}\noutput: {pattern: \"${workspace}/src\"}\nworkspace: {modules: [src]}\n",
		"src/buildy.yaml": "targets: {}\n",
	})
	err := Clean(ws, cleanContext, nil, filepath.Join(root, ".buildy_cache"), false)
	if err == nil || !strings.Contains(err.Error(), "holds the config") {
		t.Errorf("an output directory holding a module: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "buildy.yaml")); err != nil {
		t.Errorf("the module's config is gone: %v", err)
	}

	outside := t.TempDir()
	writeKeep := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(writeKeep, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	root2 := t.TempDir()
	ws2 := loadWorkspace(t, root2, map[string]string{
		"buildy.yaml": "project: {name: c}\noutput: {pattern: \"" + filepath.ToSlash(outside) + "\"}\n",
	})
	if err := Clean(ws2, cleanContext, nil, filepath.Join(root2, ".buildy_cache"), false); err != nil {
		t.Fatal(err)
	}
	if err := Clean(ws2, cleanContext, nil, outside, true); err == nil {
		t.Error("--clean-all with a cache directory outside the workspace root was accepted")
	}
	if _, err := os.Stat(writeKeep); err != nil {
		t.Errorf("clean removed a directory outside the workspace root: %v", err)
	}
}
