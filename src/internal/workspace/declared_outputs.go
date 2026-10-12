package workspace

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"buildy/pkg/util"
)

// DeclaredOutputs is what file patterns match: the outputs of the tasks generated so far, and the
// build's output directories. A pattern names the files on disk outside the output directories,
// and the declared outputs wherever they are. So a pattern over build outputs
// (${out_dir}/bin/*) names the same files on a clean build as on the next one, and never a stale
// file a previous build left in the output directory: the graph is known before anything runs.
type DeclaredOutputs struct {
	dirs    []string
	outputs map[string]bool
	sorted  []string // outputs, sorted; nil when an output was added since
}

// NewDeclaredOutputs creates an empty set.
func NewDeclaredOutputs() *DeclaredOutputs {
	return &DeclaredOutputs{outputs: map[string]bool{}}
}

// AddDir records an output directory of the build.
func (d *DeclaredOutputs) AddDir(dir string) {
	dir = filepath.Clean(dir)
	if !hasString(d.dirs, dir) {
		d.dirs = append(d.dirs, dir)
	}
}

// Add records the outputs of tasks.
func (d *DeclaredOutputs) Add(tasks ...*BuildTask) {
	for _, task := range tasks {
		for _, output := range task.Outputs {
			d.outputs[filepath.Clean(output)] = true
		}
	}
	d.sorted = nil
}

// outputDir returns the output directory path is in, or "".
func (d *DeclaredOutputs) outputDir(path string) string {
	for _, dir := range d.dirs {
		if path == dir || strings.HasPrefix(path, dir+string(filepath.Separator)) {
			return dir
		}
	}
	return ""
}

// Files returns the files pattern names, sorted: those on disk outside the output directories,
// and the declared outputs it matches. The second result is the output directory the pattern
// searches, if it starts inside one, for messages.
func (d *DeclaredOutputs) Files(g util.Glob) ([]string, string, error) {
	onDisk, err := g.Files()
	if err != nil {
		return nil, "", err
	}
	seen := map[string]bool{}
	var files []string
	for _, f := range onDisk {
		if d.outputDir(f) == "" {
			seen[f] = true
			files = append(files, f)
		}
	}
	if d.sorted == nil {
		d.sorted = make([]string, 0, len(d.outputs))
		for output := range d.outputs {
			d.sorted = append(d.sorted, output)
		}
		sort.Strings(d.sorted)
	}
	for _, output := range d.sorted {
		if !seen[output] && g.Match(output) {
			files = append(files, output)
		}
	}
	sort.Strings(files)
	return files, d.outputDir(g.Base), nil
}

// noOutputMatches is the error for a pattern into an output directory that names no declared
// output.
func noOutputMatches(g util.Glob, dir string) error {
	if g.Literal() {
		return fmt.Errorf("no task of the build produces %s (it is in the output directory %s, where only "+
			"the build's own outputs count, not files a previous build left)", g, dir)
	}
	return fmt.Errorf("'%s' matches no output of the build (it is in the output directory %s, where a "+
		"pattern names the build's own outputs, not files a previous build left)", g, dir)
}
