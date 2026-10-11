package util

import (
	"fmt"
	"strings"
)

// QuotePOSIX quotes s as one word for a POSIX shell: unchanged when it holds only characters
// that are never special, else in single quotes, where a single quote of s closes the quotes,
// is escaped with a backslash and opens them again.
func QuotePOSIX(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteCmd quotes s as one argument in a cmd.exe command line, for a program that splits its
// command line the way the C runtime does. cmd.exe has no escape that works inside quotes, so a
// value holding a double quote, a percent sign, an exclamation mark or a line break is refused
// rather than passed on changed.
func QuoteCmd(s string) (string, error) {
	if strings.ContainsAny(s, "\"%!\r\n") {
		return "", fmt.Errorf("%q cannot be passed through cmd.exe: it holds \", %%, ! or a line break", s)
	}
	if s != "" && !strings.ContainsAny(s, " \t&|<>^()") {
		return s, nil
	}
	// Backslashes before the closing quote are doubled, so they don't escape it
	trailing := len(s) - len(strings.TrimRight(s, `\`))
	return `"` + s + strings.Repeat(`\`, trailing) + `"`, nil
}

// QuoteWindowsArg quotes s as one argument of a Windows command line, by the rules the C runtime
// (and CommandLineToArgvW) use to split it again: what Go's os/exec does on Windows.
func QuoteWindowsArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\v\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
		default:
			slashes = 0
		}
		b.WriteByte(s[i])
	}
	b.WriteString(strings.Repeat(`\`, slashes))
	b.WriteByte('"')
	return b.String()
}

// JoinArgs writes an argument list as one command line, each argument quoted for the platform's
// usual shell (POSIX sh, or the Windows command line), for logs and compile_commands.json.
func JoinArgs(args []string, goos string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if goos == "windows" {
			quoted[i] = QuoteWindowsArg(a)
		} else {
			quoted[i] = QuotePOSIX(a)
		}
	}
	return strings.Join(quoted, " ")
}
