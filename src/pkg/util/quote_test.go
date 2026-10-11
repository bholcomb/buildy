package util

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

var awkward = []string{"plain", "", "a b", "it's", `say "hi"`, `C:\dir\`, `back\\slash"q`, "$HOME", "*.c", "a&b|c;d", "tab\there", "new\nline"}

// What QuotePOSIX writes, sh reads back as the same word.
func TestQuotePOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	for _, s := range awkward {
		out, err := exec.Command("sh", "-c", "printf '%s' "+QuotePOSIX(s)).Output()
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if string(out) != s {
			t.Errorf("%q quoted as %s reads back as %q", s, QuotePOSIX(s), out)
		}
	}
	if QuotePOSIX("/usr/bin/gcc") != "/usr/bin/gcc" {
		t.Error("a plain path was quoted")
	}
}

// QuoteWindowsArg follows the C runtime's rules (the cases of Go's syscall.EscapeArg tests).
func TestQuoteWindowsArg(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", `""`},
		{"a", "a"},
		{" ", `" "`},
		{`\`, `\`},
		{`\\`, `\\`},
		{`a b`, `"a b"`},
		{`a"b`, `"a\"b"`},
		{`a\"b`, `"a\\\"b"`},
		{`C:\my dir\`, `"C:\my dir\\"`},
		{`-DMSG="hello world"`, `"-DMSG=\"hello world\""`},
	} {
		if got := QuoteWindowsArg(c.in); got != c.want {
			t.Errorf("QuoteWindowsArg(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

// QuoteCmd quotes for cmd.exe, refusing what cmd.exe would change inside quotes.
func TestQuoteCmd(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a", "a"},
		{"", `""`},
		{`C:\my dir\a.c`, `"C:\my dir\a.c"`},
		{`C:\my dir\`, `"C:\my dir\\"`},
		{"a&b", `"a&b"`},
		{"it's", "it's"},
	} {
		got, err := QuoteCmd(c.in)
		if err != nil || got != c.want {
			t.Errorf("QuoteCmd(%q) = %s, %v; want %s", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{`say "hi"`, "100%", "wow!", "a\nb"} {
		if _, err := QuoteCmd(bad); err == nil {
			t.Errorf("QuoteCmd(%q) was accepted", bad)
		}
	}
}

func TestJoinArgs(t *testing.T) {
	args := []string{"gcc", "-DMSG=\"a b\"", "/my dir/x.c"}
	if got := JoinArgs(args, "linux"); got != `gcc '-DMSG="a b"' '/my dir/x.c'` {
		t.Errorf("POSIX: %s", got)
	}
	if got := JoinArgs(args, "windows"); got != `gcc "-DMSG=\"a b\"" "/my dir/x.c"` {
		t.Errorf("Windows: %s", got)
	}
	if strings.Contains(JoinArgs([]string{"a"}, "linux"), "'") {
		t.Error("a plain argument was quoted")
	}
}
