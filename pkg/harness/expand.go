package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultMaxShellOutput = 32 << 10
	DefaultMaxFileOutput  = 64 << 10

	shellTimeout = 15 * time.Second

	ArgumentsLabel = "ARGUMENTS:"
)

var placeholderRe = regexp.MustCompile("(?s)!`([^`]+)`|\\$(ARGUMENTS|[0-9]+)|@([A-Za-z0-9_./-]+)")

type ExpandOptions struct {
	WorkDir            string
	AllowShell         bool
	MaxShellOutput     int
	MaxFileOutput      int
	AllowAbsoluteFiles bool
}

func Expand(cmd *Command, rawArgs string, opts ExpandOptions) (string, error) {
	if cmd == nil {
		return "", fmt.Errorf("harness: Expand called with nil command")
	}
	if opts.MaxShellOutput <= 0 {
		opts.MaxShellOutput = DefaultMaxShellOutput
	}
	if opts.MaxFileOutput <= 0 {
		opts.MaxFileOutput = DefaultMaxFileOutput
	}
	if opts.WorkDir == "" {
		opts.WorkDir, _ = os.Getwd()
	}

	out, coverage, err := expandTemplate(cmd.Template, rawArgs, opts)
	if err != nil {
		return "", err
	}
	if !coverage.complete() && strings.TrimSpace(rawArgs) != "" {
		out = strings.TrimRight(out, " \t\r\n") + "\n\n" + ArgumentsLabel + " " + rawArgs
	}
	return out, nil
}

type argCoverage struct {
	all    bool
	tokens int
	used   map[int]bool
}

func (c argCoverage) complete() bool {
	if c.all {
		return true
	}
	for i := 1; i <= c.tokens; i++ {
		if !c.used[i] {
			return false
		}
	}
	return true
}

func expandTemplate(tmpl, rawArgs string, opts ExpandOptions) (string, argCoverage, error) {
	args := tokenizeArgs(rawArgs)
	coverage := argCoverage{tokens: len(args), used: map[int]bool{}}

	var b strings.Builder
	b.Grow(len(tmpl) + 64)
	last := 0
	for _, m := range placeholderRe.FindAllStringSubmatchIndex(tmpl, -1) {
		start, end := m[0], m[1]
		if m[6] >= 0 && start > 0 {
			prev := tmpl[start-1]
			if prev == ':' || prev == '_' || isWordByte(prev) {
				continue
			}
		}
		b.WriteString(tmpl[last:start])
		switch {
		case m[2] >= 0:
			b.WriteString(expandShell(tmpl[m[2]:m[3]], opts))
		case m[4] >= 0:
			token := tmpl[m[4]:m[5]]
			if takesArguments(token) {
				if token == "ARGUMENTS" {
					coverage.all = true
				} else {
					coverage.used[int(token[0]-'0')] = true
				}
			}
			b.WriteString(expandArg(token, rawArgs, args))
		default:
			b.WriteString(readFileRef(tmpl[m[6]:m[7]], opts))
		}
		last = end
	}
	b.WriteString(tmpl[last:])
	return b.String(), coverage, nil
}

func expandShell(command string, opts ExpandOptions) string {
	if !opts.AllowShell {
		return "[shell disabled]"
	}
	return runShell(command, opts)
}

func expandArg(token, rawArgs string, args []string) string {
	if token == "ARGUMENTS" {
		return rawArgs
	}
	if !takesArguments(token) {
		return "$" + token
	}
	idx := token[0] - '1'
	if int(idx) < len(args) {
		return args[idx]
	}
	return ""
}

func takesArguments(token string) bool {
	if token == "ARGUMENTS" {
		return true
	}
	return len(token) == 1 && token[0] >= '1' && token[0] <= '9'
}

func runShell(command string, opts ExpandOptions) string {
	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()

	c := exec.CommandContext(ctx, "sh", "-c", command)
	c.Dir = opts.WorkDir
	detachShellChild(c)
	c.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		"PAGER=cat",
		"DEBIAN_FRONTEND=noninteractive",
	)
	out, err := c.CombinedOutput()
	text := strings.TrimRight(string(out), " \t\r\n")
	if err != nil {
		if text != "" {
			return fmt.Sprintf("[error: %v]\n%s", err, capOutput(text, opts.MaxShellOutput))
		}
		return fmt.Sprintf("[error: %v]", err)
	}
	return capOutput(text, opts.MaxShellOutput)
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func readFileRef(path string, opts ExpandOptions) string {
	if filepath.IsAbs(path) {
		if !opts.AllowAbsoluteFiles {
			return "[blocked: absolute path]"
		}
		if _, err := os.Stat(path); err != nil {
			return "[missing: @" + path + "]"
		}
		return readCapped(path, opts)
	}

	joined := filepath.Join(opts.WorkDir, path)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "[error: @" + path + "]"
	}
	rel, err := filepath.Rel(opts.WorkDir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "[outside workspace: @" + path + "]"
	}
	if _, err := os.Stat(abs); err != nil {
		return "[missing: @" + path + "]"
	}
	return readCapped(abs, opts)
}

func readCapped(path string, opts ExpandOptions) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "[missing: @" + path + "]"
	}
	return capOutput(strings.TrimSpace(string(raw)), opts.MaxFileOutput)
}

func capOutput(s string, limit int) string {
	s = strings.TrimRight(s, " \t\r\n")
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := s[:limit]
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n...[truncated]"
}

func tokenizeArgs(s string) []string {
	var (
		tokens  []string
		cur     strings.Builder
		inToken bool
		inSgl   bool
		inDbl   bool
	)
	flush := func() {
		if inToken {
			tokens = append(tokens, cur.String())
			cur.Reset()
			inToken = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inSgl:
			if c == '\'' {
				inSgl = false
			} else {
				cur.WriteByte(c)
			}
		case inDbl:
			switch {
			case c == '"':
				inDbl = false
			case c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\'):
				i++
				cur.WriteByte(s[i])
			default:
				cur.WriteByte(c)
			}
		case c == '\'':
			inSgl, inToken = true, true
		case c == '"':
			inDbl, inToken = true, true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		default:
			inToken = true
			cur.WriteByte(c)
		}
	}
	flush()
	return tokens
}
