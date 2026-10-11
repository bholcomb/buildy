package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"buildy/pkg/util"
)

func TestResponseFileContent(t *testing.T) {
	args := []string{"rcs", "/my ws/it's.a", `-DMSG="hi there"`, `C:\dir\`, ""}
	gnu, err := responseFileContent(args, util.ResponseFileGNU)
	if err != nil {
		t.Fatal(err)
	}
	want := "rcs\n/my\\ ws/it\\'s.a\n-DMSG=\\\"hi\\ there\\\"\nC:\\\\dir\\\\\n''\n"
	if string(gnu) != want {
		t.Errorf("gnu:\n%s\nwant\n%s", gnu, want)
	}

	msvc, err := responseFileContent(args[1:4], util.ResponseFileMSVC)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\"/my ws/it's.a\"\r\n\"-DMSG=\\\"hi there\\\"\"\r\nC:\\dir\\\r\n"; string(msvc) != want {
		t.Errorf("msvc:\n%q\nwant\n%q", msvc, want)
	}
	unicode, _ := responseFileContent([]string{"/Fo:café.obj"}, util.ResponseFileMSVC)
	if len(unicode) < 2 || unicode[0] != 0xff || unicode[1] != 0xfe || unicode[2] != '/' || unicode[3] != 0 {
		t.Errorf("a non-ASCII MSVC response file is not UTF-16 with a BOM: % x", unicode[:min(8, len(unicode))])
	}
	if _, err := responseFileContent(args, "rsp"); err == nil {
		t.Error("an unknown syntax was accepted")
	}
}

// GCC reads the arguments of a gnu response file back as written.
func TestGNUResponseFileRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil || runtime.GOOS == "windows" {
		t.Skip("needs gcc")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, `it's a "dir"`, "main.c")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("#include <stdio.h>\nint main(void) { puts(MSG); return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "out dir", "app")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	rsp := filepath.Join(dir, "args.rsp")
	content, _ := responseFileContent([]string{`-DMSG="tab\there 'quoted' \\ back"`, src, "-o", exe}, util.ResponseFileGNU)
	if err := os.WriteFile(rsp, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("gcc", "@"+rsp).CombinedOutput(); err != nil {
		t.Fatalf("gcc @%s: %v\n%s", rsp, err, out)
	}
	out, err := exec.Command(exe).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "tab\there 'quoted' \\ back" {
		t.Errorf("the program printed %q", got)
	}
}

// Finding 2.2: a command over the host's limit runs with its arguments in a response file; one
// whose tool takes none, or a shell script, fails naming the limit.
func TestWithResponseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rsp", "task.rsp")
	short := util.Command{Args: []string{"ar", "rcs", "lib.a", "a.o"}, ResponseFile: util.ResponseFileGNU}
	cmd, remove, err := withResponseFile(short, nil, path)
	if err != nil || strings.Join(cmd.Args, " ") != "ar rcs lib.a a.o" {
		t.Errorf("a short command became %q, %v", cmd.Args, err)
	}
	remove()

	huge := strings.Repeat("x", 2<<20) // over every host's limit
	long := util.Command{Args: []string{"ar", "rcs", "lib.a", huge}, ResponseFile: util.ResponseFileGNU, Dir: "/d"}
	cmd, remove, err = withResponseFile(long, nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "ar" || cmd.Args[1] != "@"+path || cmd.Dir != "/d" {
		t.Errorf("a long command became %q", cmd.Args)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "rcs\nlib.a\n"+huge+"\n" {
		t.Errorf("response file: %v", err)
	}
	remove()
	if _, err := os.Stat(path); err == nil {
		t.Error("the response file was not removed")
	}

	long.ResponseFile = ""
	if _, _, err := withResponseFile(long, nil, path); err == nil || !strings.Contains(err.Error(), "response_file") {
		t.Errorf("a long command without a response file: %v", err)
	}
	if _, _, err := withResponseFile(util.Command{Script: "echo " + huge}, nil, path); err == nil || !strings.Contains(err.Error(), "shell") {
		t.Errorf("a long script: %v", err)
	}
}

// The executor runs a long command with its response file, and removes the file afterwards.
func TestExecutorUsesResponseFiles(t *testing.T) {
	dir := t.TempDir()
	te := newExecutor(t, dir, false)
	out := filepath.Join(dir, "lib.a")
	var mu sync.Mutex
	var ran util.Command
	var rspContent string
	te.runCommand = func(spec util.ExecSpec, command util.Command, workDir string, m []string, timeout int) (*util.ExecutionResult, error) {
		mu.Lock()
		defer mu.Unlock()
		ran = command
		if len(command.Args) == 2 && strings.HasPrefix(command.Args[1], "@") {
			data, _ := os.ReadFile(command.Args[1][1:])
			rspContent = string(data)
		}
		return util.Run(spec, helperCommand(t, "write", out, "archive"), workDir, nil, timeout)
	}
	huge := strings.Repeat("o", 2<<20)
	task := newTask("link_big", nil, []string{out}, util.Command{Args: []string{"ar", "rcs", out, huge}, ResponseFile: util.ResponseFileGNU})
	if !runTasks(te, task) {
		t.Fatal("task failed")
	}
	if len(ran.Args) != 2 || !strings.HasPrefix(rspContent, "rcs\n") || !strings.Contains(rspContent, huge) {
		t.Errorf("ran %q with response file %d bytes", ran.Args, len(rspContent))
	}
	if _, err := os.Stat(ran.Args[1][1:]); err == nil {
		t.Error("the response file was left behind")
	}
}
