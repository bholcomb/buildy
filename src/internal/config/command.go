package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// CommandTemplate is a command line as a toolchain or a build system writes it:
// "gcc ${flags} -c ${input} -o ${output}". It is split into words when it is loaded, before any
// value is known, and run as an argument list, without a shell (doc/design/commands.md):
//
//   - Words are separated by spaces, tabs and newlines. Single quotes keep everything inside
//     them as written, '$ORIGIN' and '${x}' included; double quotes keep spaces in a word and
//     still fill placeholders in. A backslash is an ordinary character.
//   - A word that is a placeholder and nothing else, ${includes}, becomes the placeholder's
//     values, each one argument: none for an empty value, several for a list. A path with
//     spaces stays one argument.
//   - A placeholder inside a word, -I${dir} or /OUT:${output} or "${x}", is replaced by its
//     value as text, and the word stays one argument. Such a placeholder must have exactly one
//     value; an empty or several-valued one there is an error.
type CommandTemplate struct {
	Text         string
	words        []commandWord
	placeholders []placeholderRef
}

// commandWord is a word of a template: one argument, or a lone placeholder's arguments.
type commandWord struct {
	parts []commandPart
	alone bool // one unquoted placeholder: it expands to its values, each an argument
}

// commandPart is a piece of a word: literal text, or a placeholder.
type commandPart struct {
	literal string
	name    string // the placeholder's name; "" for literal text
}

// placeholderRef is where a placeholder is in Text, for commands run by a shell.
type placeholderRef struct {
	name       string
	start, end int  // Text[start:end] is "${name}"
	alone      bool // the whole word: it expands to its values, each an argument
	quoted     bool // inside double quotes
}

var placeholderName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// ParseCommandTemplate splits a command into words. It fails on an unterminated quote or
// placeholder, an invalid placeholder name, or a command with no words.
func ParseCommandTemplate(text string) (CommandTemplate, error) {
	t := CommandTemplate{Text: text}
	var word []commandPart
	inWord := false // a word has started (an empty "" is a word)
	quotes := false // the current word has quotes: it is never a lone placeholder
	var quote byte  // the open quote, 0 outside quotes
	var literal []byte

	flushLiteral := func() {
		if len(literal) > 0 {
			word = append(word, commandPart{literal: string(literal)})
			literal = literal[:0]
		}
	}
	endWord := func() {
		if !inWord {
			return
		}
		flushLiteral()
		alone := !quotes && len(word) == 1 && word[0].name != ""
		if alone {
			t.placeholders[len(t.placeholders)-1].alone = true
		}
		t.words = append(t.words, commandWord{parts: word, alone: alone})
		word, inWord, quotes = nil, false, false
	}

	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				literal = append(literal, c)
			}
		case c == '$' && i+1 < len(text) && text[i+1] == '{':
			end := strings.IndexByte(text[i:], '}')
			if end < 0 {
				return CommandTemplate{}, fmt.Errorf("unterminated placeholder at %q", text[i:])
			}
			name := text[i+2 : i+end]
			if !placeholderName.MatchString(name) {
				return CommandTemplate{}, fmt.Errorf("'${%s}' is not a placeholder: a name is letters, digits, '_' and '.' (quote it as '${%s}' to pass it as written)", name, name)
			}
			inWord = true
			flushLiteral()
			word = append(word, commandPart{name: name})
			t.placeholders = append(t.placeholders, placeholderRef{name: name, start: i, end: i + end + 1, quoted: quote == '"'})
			i += end
		case quote == '"':
			if c == '"' {
				quote = 0
			} else {
				literal = append(literal, c)
			}
		case c == '\'' || c == '"':
			quote, inWord, quotes = c, true, true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			endWord()
		default:
			inWord = true
			literal = append(literal, c)
		}
	}
	if quote != 0 {
		return CommandTemplate{}, fmt.Errorf("unterminated %c quote", quote)
	}
	endWord()
	if len(t.words) == 0 {
		return CommandTemplate{}, fmt.Errorf("the command is empty")
	}
	return t, nil
}

// IsZero reports whether the template is unset.
func (t CommandTemplate) IsZero() bool {
	return t.Text == "" && len(t.words) == 0
}

// Placeholders returns the names of the template's placeholders, in order, without repeats.
func (t CommandTemplate) Placeholders() []string {
	var names []string
	seen := map[string]bool{}
	for _, p := range t.placeholders {
		if !seen[p.name] {
			seen[p.name] = true
			names = append(names, p.name)
		}
	}
	return names
}

// HasQuotedPlaceholder reports whether a placeholder is written inside double quotes.
func (t CommandTemplate) HasQuotedPlaceholder() bool {
	for _, p := range t.placeholders {
		if p.quoted {
			return true
		}
	}
	return false
}

// Values gives a placeholder's values, each one argument, and whether it has any value at all
// (an unknown placeholder is an error; an empty list is not).
type Values func(name string) ([]string, bool)

// MapValues looks placeholders up in a map.
func MapValues(m map[string][]string) Values {
	return func(name string) ([]string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

// Expand returns the template's argument list with every placeholder filled in.
func (t CommandTemplate) Expand(values Values) ([]string, error) {
	var args []string
	for _, word := range t.words {
		if word.alone {
			v, err := lookup(values, word.parts[0].name)
			if err != nil {
				return nil, err
			}
			args = append(args, v...)
			continue
		}
		var b strings.Builder
		for _, part := range word.parts {
			if part.name == "" {
				b.WriteString(part.literal)
				continue
			}
			v, err := single(values, part.name, word.parts)
			if err != nil {
				return nil, err
			}
			b.WriteString(v)
		}
		args = append(args, b.String())
	}
	if len(args) == 0 || args[0] == "" {
		return nil, fmt.Errorf("%q expands to no program to run", t.Text)
	}
	return args, nil
}

// ExpandShell returns the template as a shell script: the text as written, with each placeholder
// replaced by its values, each quoted by quote. A placeholder alone as a word gives its values
// separated by spaces; one inside a word must have exactly one value. Placeholders inside double
// quotes are rejected when the tool is loaded.
func (t CommandTemplate) ExpandShell(values Values, quote func(string) (string, error)) (string, error) {
	var b strings.Builder
	prev := 0
	for _, p := range t.placeholders {
		b.WriteString(t.Text[prev:p.start])
		prev = p.end
		if p.quoted {
			return "", fmt.Errorf("placeholder ${%s} is inside double quotes in a shell command: write it outside quotes, buildy quotes its values", p.name)
		}
		var v []string
		if p.alone {
			var err error
			if v, err = lookup(values, p.name); err != nil {
				return "", err
			}
		} else {
			s, err := single(values, p.name, nil)
			if err != nil {
				return "", err
			}
			v = []string{s}
		}
		for i, value := range v {
			q, err := quote(value)
			if err != nil {
				return "", fmt.Errorf("${%s}: %w", p.name, err)
			}
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(q)
		}
	}
	b.WriteString(t.Text[prev:])
	return strings.TrimSpace(b.String()), nil
}

func lookup(values Values, name string) ([]string, error) {
	v, ok := values(name)
	if !ok {
		return nil, &MissingPlaceholderError{Name: name}
	}
	var args []string
	for _, s := range v {
		if s != "" {
			args = append(args, s)
		}
	}
	return args, nil
}

func single(values Values, name string, word []commandPart) (string, error) {
	v, err := lookup(values, name)
	if err != nil {
		return "", err
	}
	if len(v) != 1 {
		where := ""
		if word != nil {
			where = " in '" + wordText(word) + "'"
		}
		what := "is empty"
		if len(v) > 1 {
			what = fmt.Sprintf("has %d values (%s)", len(v), strings.Join(v, " "))
		}
		return "", fmt.Errorf("${%s}%s %s: a placeholder inside a word must have exactly one value (write it as a word of its own to pass any number of arguments)", name, where, what)
	}
	return v[0], nil
}

func wordText(word []commandPart) string {
	var b strings.Builder
	for _, part := range word {
		if part.name != "" {
			b.WriteString("${" + part.name + "}")
		} else {
			b.WriteString(part.literal)
		}
	}
	return b.String()
}

// MissingPlaceholderError is a placeholder that was given no value.
type MissingPlaceholderError struct {
	Name string
}

func (e *MissingPlaceholderError) Error() string {
	return fmt.Sprintf("no value for ${%s}", e.Name)
}

func (t *CommandTemplate) decodeNode(d *decoder, n *yaml.Node, path string) {
	if isNull(n) {
		return
	}
	text, ok := d.scalarString(n, path)
	if !ok || text == "" {
		return
	}
	parsed, err := ParseCommandTemplate(text)
	if err != nil {
		d.errorf(n, "'%s': %v", path, err)
		return
	}
	*t = parsed
}

// MarshalJSON writes the template as written (toolchain hashes and the tasks file).
func (t CommandTemplate) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Text)
}
