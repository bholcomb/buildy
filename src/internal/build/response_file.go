package build

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"buildy/pkg/util"
)

// The host: where commands run (a Docker task's docker client too).
var (
	hostIsWindows = runtime.GOOS == "windows"
	hostIsLinux   = runtime.GOOS == "linux"
)

// maxArgLen is the longest single argument Linux passes to a program (MAX_ARG_STRLEN, 32 pages,
// with its terminating NUL).
const maxArgLen = 128*1024 - 1

// commandLineFits reports whether a command's arguments, with env, fit what the host lets a
// program be started with, and the size and limit it compared.
//
//   - Linux: the arguments and environment, strings and pointers, within the limit the stack size
//     sets (a quarter of it, between 128 KiB and 6 MiB), and no argument over 128 KiB;
//   - macOS: the same within kern.argmax (1 MiB);
//   - Windows: the command line CreateProcess gets, under 32767 characters.
//
// Three quarters of the Linux and macOS limits are used: the kernel also counts things buildy
// doesn't see.
func commandLineFits(args, env []string) (fits bool, size, limit int) {
	if hostIsWindows {
		line := util.JoinArgs(args, "windows")
		size = len(utf16.Encode([]rune(line))) + 1
		return size <= windowsCommandLineMax, size, windowsCommandLineMax
	}
	const pointer = 8
	for _, list := range [][]string{args, env} {
		for _, s := range list {
			if len(s) > maxArgLen && hostIsLinux {
				return false, len(s), maxArgLen
			}
			size += len(s) + 1 + pointer
		}
	}
	limit = argMax() / 4 * 3
	return size <= limit, size, limit
}

// windowsCommandLineMax leaves room below CreateProcess's 32767 characters.
const windowsCommandLineMax = 32000

// withResponseFile returns the command to run for a task: as it is when its arguments fit the
// host's limits, else with the arguments after the program in a response file, in the syntax
// the tool declared, at path. It fails when the command doesn't fit and the tool takes no
// response file. remove deletes the file, if one was written.
func withResponseFile(command util.Command, env []string, path string) (util.Command, func(), error) {
	noop := func() {}
	args := command.Args
	if command.Script != "" {
		args = []string{"sh", "-c", command.Script}
	}
	fits, size, limit := commandLineFits(args, env)
	if fits {
		return command, noop, nil
	}
	if command.Script != "" {
		return util.Command{}, noop, fmt.Errorf("its shell command is %d bytes, over the limit of %d, and a shell tool can't take a response file", size, limit)
	}
	if command.ResponseFile == "" {
		return util.Command{}, noop, fmt.Errorf("its command line is %d bytes, over the limit of %d, and its tool takes no response file (declare response_file in the toolchain's tool)", size, limit)
	}
	content, err := responseFileContent(command.Args[1:], command.ResponseFile)
	if err != nil {
		return util.Command{}, noop, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return util.Command{}, noop, err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return util.Command{}, noop, fmt.Errorf("writing response file: %w", err)
	}
	util.LogInfo("  Arguments in response file %s (%d bytes on the command line, over the limit of %d)", path, size, limit)
	command.Args = []string{command.Args[0], "@" + path}
	return command, func() { os.Remove(path) }, nil
}

// responseFileContent writes arguments one per line, quoted for the syntax:
//
//   - gnu (GCC, Clang, GNU ar and ld): every whitespace character, quote and backslash is escaped
//     with a backslash; an empty argument is ”.
//   - msvc (cl, link, lib): each argument quoted as on a Windows command line. A file with
//     characters outside ASCII is UTF-16 with a byte order mark, which the MSVC tools read as
//     Unicode; otherwise it is plain text.
func responseFileContent(args []string, syntax string) ([]byte, error) {
	var lines []string
	switch syntax {
	case util.ResponseFileGNU:
		for _, a := range args {
			lines = append(lines, quoteGNUResponse(a))
		}
		return []byte(strings.Join(lines, "\n") + "\n"), nil
	case util.ResponseFileMSVC:
		for _, a := range args {
			lines = append(lines, util.QuoteWindowsArg(a))
		}
		text := strings.Join(lines, "\r\n") + "\r\n"
		for _, r := range text {
			if r > 0x7f {
				return utf16WithBOM(text), nil
			}
		}
		return []byte(text), nil
	}
	return nil, fmt.Errorf("unknown response file syntax %q", syntax)
}

func quoteGNUResponse(arg string) string {
	if arg == "" {
		return "''"
	}
	var b strings.Builder
	for i := 0; i < len(arg); i++ {
		switch arg[i] {
		case ' ', '\t', '\n', '\r', '\v', '\f', '\'', '"', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(arg[i])
	}
	return b.String()
}

func utf16WithBOM(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := make([]byte, 0, 2+2*len(units))
	out = append(out, 0xff, 0xfe)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}
