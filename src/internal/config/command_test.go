package config

import (
	"strings"
	"testing"

	"buildy/pkg/util"
)

func TestCommandTemplateExpand(t *testing.T) {
	values := MapValues(map[string][]string{
		"input":    {"/ws/my src/main.c"},
		"output":   {"/ws/build/main.o"},
		"includes": {"-I/ws/inc dir", "-I/ws/gen"},
		"flags":    {},
		"pic":      {""},
		"std":      {"c11"},
		"defines":  {`-DMSG="hello world"`},
		"dir":      {"/opt/it's here"},
	})
	cases := []struct {
		template string
		want     []string
	}{
		// A lone placeholder gives its values, each one argument; an empty one gives none.
		{"gcc ${flags} ${pic} -std=${std} ${defines} ${includes} -c ${input} -o ${output}",
			[]string{"gcc", "-std=c11", `-DMSG="hello world"`, "-I/ws/inc dir", "-I/ws/gen", "-c", "/ws/my src/main.c", "-o", "/ws/build/main.o"}},
		// Inside a word, a placeholder is text: the word stays one argument.
		{"cl.exe /c ${input} /Fo${output} -L${dir}", []string{"cl.exe", "/c", "/ws/my src/main.c", "/Fo/ws/build/main.o", "-L/opt/it's here"}},
		// Single quotes keep $ORIGIN and ${...} as written; double quotes keep spaces and fill placeholders.
		{`ld '-rpath=$ORIGIN' -Wl,-rpath,$ORIGIN '${output}' "a b" "x=${std}"`, []string{"ld", "-rpath=$ORIGIN", "-Wl,-rpath,$ORIGIN", "${output}", "a b", "x=c11"}},
		// A quoted lone placeholder is one argument; an empty quoted word is an empty argument.
		{`tool "${input}" ""`, []string{"tool", "/ws/my src/main.c", ""}},
		// Backslashes are ordinary characters; words split on any whitespace.
		{"C:\\tools\\gen.exe\t\\\\server\\share\n${output}", []string{`C:\tools\gen.exe`, `\\server\share`, "/ws/build/main.o"}},
	}
	for _, c := range cases {
		tmpl, err := ParseCommandTemplate(c.template)
		if err != nil {
			t.Errorf("%q: %v", c.template, err)
			continue
		}
		got, err := tmpl.Expand(values)
		if err != nil {
			t.Errorf("%q: %v", c.template, err)
			continue
		}
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%q\n  got  %q\n  want %q", c.template, got, c.want)
		}
	}
}

func TestCommandTemplateErrors(t *testing.T) {
	for _, c := range []struct{ template, want string }{
		{"gcc 'unterminated", "unterminated ' quote"},
		{`gcc "unterminated`, `unterminated " quote`},
		{"gcc ${input", "unterminated placeholder"},
		{"gcc ${in put}", "is not a placeholder"},
		{"   ", "the command is empty"},
	} {
		if _, err := ParseCommandTemplate(c.template); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %v, want %q", c.template, err, c.want)
		}
	}

	values := MapValues(map[string][]string{"includes": {"-Ia", "-Ib"}, "empty": nil, "tool": {}})
	for _, c := range []struct{ template, want string }{
		{"gcc ${nope}", "no value for ${nope}"},
		{"gcc -X${includes}", "${includes} in '-X${includes}' has 2 values"},
		{"gcc -std=${empty}", "${empty} in '-std=${empty}' is empty"},
		{`gcc "${includes}"`, "has 2 values"},
		{"${tool} ${empty}", "expands to no program"},
	} {
		tmpl := MustParseCommandTemplate(c.template)
		if _, err := tmpl.Expand(values); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %v, want %q", c.template, err, c.want)
		}
	}
}

// A shell tool's command is its text as written, with each value quoted for the shell.
func TestCommandTemplateExpandShell(t *testing.T) {
	values := MapValues(map[string][]string{
		"input":   {"/ws/it's in.c"},
		"objects": {"/ws/a.o", "/ws/b c.o"},
		"output":  {"/ws/out"},
		"none":    {},
	})
	posix := func(s string) (string, error) { return util.QuotePOSIX(s), nil }
	tmpl := MustParseCommandTemplate(`test -f /.dockerenv && cat ${objects} ${none} > ${output} && gen -i${input} | sed 's/${x}/y/'`)
	got, err := tmpl.ExpandShell(values, posix)
	if err != nil {
		t.Fatal(err)
	}
	want := `test -f /.dockerenv && cat /ws/a.o '/ws/b c.o'  > /ws/out && gen -i'/ws/it'\''s in.c' | sed 's/${x}/y/'`
	if got != want {
		t.Errorf("got\n  %s\nwant\n  %s", got, want)
	}

	quoted := MustParseCommandTemplate(`echo "${input}"`)
	if !quoted.HasQuotedPlaceholder() {
		t.Error("a placeholder in double quotes was not noticed")
	}
	if _, err := quoted.ExpandShell(values, posix); err == nil {
		t.Error("a placeholder in double quotes was accepted in a shell command")
	}
	if _, err := MustParseCommandTemplate("cat ${input}").ExpandShell(values, util.QuoteCmd); err != nil {
		t.Errorf("cmd.exe quoting of a path with a quote-like character: %v", err)
	}
	if _, err := MustParseCommandTemplate("echo ${input}").ExpandShell(MapValues(map[string][]string{"input": {"100%"}}), util.QuoteCmd); err == nil {
		t.Error("a value cmd.exe would expand was accepted")
	}
}
