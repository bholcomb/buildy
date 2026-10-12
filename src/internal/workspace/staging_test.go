package workspace

import (
	"strings"
	"testing"
)

const symlinkStaging = `
project: {name: s}
staging:
  name: pkg
  use_symlinks: true
  contents:
    - folder: docs
      files: ["readme.txt"]
install:
  - {name: pkg, staging: pkg%s}
`

// Whether staging makes a symlink depends on the host, not on the platform being built for.
func TestStagingSymlinksFollowTheHost(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"buildy.yaml": strings.Replace(symlinkStaging, "%s", "", 1), "readme.txt": "hi"})
	saved := hostCanSymlink
	defer func() { hostCanSymlink = saved }()

	for _, host := range []bool{true, false} {
		hostCanSymlink = host
		for _, platform := range []string{"linux", "windows"} {
			tasks, err := generateTasksFor(t, root, platform, "x86_64", "debug")
			if err != nil {
				t.Fatal(err)
			}
			want := NativeCopy
			if host {
				want = NativeSymlink
			}
			if got := len(tasksOfType(tasks, want)); got != 1 {
				t.Errorf("host can symlink: %v, building for %s: %d %s tasks, want 1", host, platform, got, want)
			}
		}
	}
}

// A package for Windows stores what links point to: follow_symlinks: false is an error there.
func TestWindowsPackageFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"buildy.yaml": strings.Replace(symlinkStaging, "%s", ", follow_symlinks: false", 1), "readme.txt": "hi"})
	if _, err := generateTasksFor(t, root, "linux", "x86_64", "debug"); err != nil {
		t.Errorf("a linux package keeping links: %v", err)
	}
	if _, err := generateTasksFor(t, root, "windows", "x86_64", "debug"); err == nil || !strings.Contains(err.Error(), "follow_symlinks") {
		t.Errorf("a windows package keeping links gave %v", err)
	}
}
