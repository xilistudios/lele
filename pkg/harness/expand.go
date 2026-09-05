// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

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

// Default output caps, applied when the corresponding ExpandOptions field is
// left at its zero value.
const (
	DefaultMaxShellOutput = 32 << 10 // 32 KiB of command output
	DefaultMaxFileOutput  = 64 << 10 // 64 KiB of inlined file content

	// shellTimeout bounds a single !`cmd` execution.
	shellTimeout = 15 * time.Second
)

// placeholderRe matches, in one pass, every template placeholder:
//   - group 1: opencode-style inline shell !`command`
//   - group 2: $ARGUMENTS or a $<digits> run
//   - group 3: @path file reference
//
// A single left-to-right pass is what keeps substitution order safe:
// regexp replacement text is never rescanned, so content injected from
// arguments, files or shell output can never trigger a second round of
// substitutions (no shell smuggling through $ARGUMENTS, no re-expansion of
// "$1" appearing inside an inlined file).
//
// The preceding-character rule for @ (skip if the char before "@" is a word
// char, "_" or ":") is applied in code because RE2 has no lookbehind; it keeps
// e-mail addresses and @mentions glued to words from being mistaken for file
// references.
var placeholderRe = regexp.MustCompile("(?s)!`([^`]+)`|\\$(ARGUMENTS|[0-9]+)|@([A-Za-z0-9_./-]+)")

// ExpandOptions tunes expansion. Zero values mean "use defaults":
// MaxShellOutput/MaxFileOutput <= 0 fall back to the Default* caps and an
// empty WorkDir falls back to the process working directory.
type ExpandOptions struct {
	WorkDir        string
	AllowShell     bool
	MaxShellOutput int
	MaxFileOutput  int
}

// Expand renders cmd.Template with the user's raw arguments. Substitution
// order matters for security: !`cmd` and @file are resolved on the template
// only, and $ARGUMENTS / $1..$9 are injected LAST, so neither arguments nor
// inlined file content can smuggle a second round of shell execution or
// further substitutions.
//
// Substitution failures never return an error — they become visible
// placeholders ("[shell disabled]", "[missing: @p]", "[error: ...]") so the
// model can tell what happened. The only hard error is a nil command.
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

	out, err := expandTemplate(cmd.Template, rawArgs, opts)
	if err != nil {
		return "", err
	}
	return out, nil
}

// expandTemplate performs the single-pass substitution described on Expand.
func expandTemplate(tmpl, rawArgs string, opts ExpandOptions) (string, error) {
	args := tokenizeArgs(rawArgs)

	var b strings.Builder
	b.Grow(len(tmpl) + 64)
	last := 0
	for _, m := range placeholderRe.FindAllStringSubmatchIndex(tmpl, -1) {
		start, end := m[0], m[1]
		// Guard @ references: skip if glued to a word char, "_" or ":"
		// (e-mails, URLs, mentions). Skipped matches stay verbatim because
		// their text is written by the next segment copy.
		if m[6] >= 0 && start > 0 {
			prev := tmpl[start-1]
			if prev == ':' || prev == '_' || isWordByte(prev) {
				continue
			}
		}
		b.WriteString(tmpl[last:start])
		switch {
		case m[2] >= 0: // !`cmd`
			b.WriteString(expandShell(tmpl[m[2]:m[3]], opts))
		case m[4] >= 0: // $ARGUMENTS / $<digits>
			b.WriteString(expandArg(tmpl[m[4]:m[5]], rawArgs, args))
		default: // @path
			b.WriteString(readFileRef(tmpl[m[6]:m[7]], opts))
		}
		last = end
	}
	b.WriteString(tmpl[last:])
	return b.String(), nil
}

// expandShell resolves one !`cmd` snippet: either executed output or, when
// shell injection is disabled, a visible placeholder.
func expandShell(command string, opts ExpandOptions) string {
	if !opts.AllowShell {
		return "[shell disabled]"
	}
	return runShell(command, opts)
}

// expandArg substitutes a "$..." token: $ARGUMENTS gets the raw argument
// string verbatim; $<digits> maps to a positional token (single digit 1..9
// only — "$0" and "$10" and beyond stay verbatim); positionals beyond the
// arguments provided expand to the empty string.
func expandArg(token, rawArgs string, args []string) string {
	if token == "ARGUMENTS" {
		return rawArgs
	}
	if len(token) != 1 || token[0] < '1' || token[0] > '9' {
		return "$" + token // $0, $10, ... untouched
	}
	idx := token[0] - '1'
	if int(idx) < len(args) {
		return args[idx]
	}
	return "" // positional beyond the args provided => empty
}

// runShell executes one !`cmd` snippet with sh -c in WorkDir and returns the
// text to inject. On failure any partial output is still injected, prefixed
// with the error, so the model sees what the command managed to print.
func runShell(command string, opts ExpandOptions) string {
	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()

	c := exec.CommandContext(ctx, "sh", "-c", command)
	c.Dir = opts.WorkDir
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

// isWordByte reports whether the byte is ASCII [0-9A-Za-z], the byte-level
// equivalent of \w for the ASCII-only templates we care about.
func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// readFileRef resolves and reads one @path reference. Absolute paths are used
// as-is only when they exist; relative paths resolve against WorkDir and may
// not escape it.
func readFileRef(path string, opts ExpandOptions) string {
	if filepath.IsAbs(path) {
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

// readCapped reads a file and caps it at limit bytes, appending a truncation
// marker when the cap kicks in.
func readCapped(path string, opts ExpandOptions) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "[missing: @" + path + "]"
	}
	return capOutput(strings.TrimSpace(string(raw)), opts.MaxFileOutput)
}

// capOutput trims trailing whitespace and cuts the string at limit bytes
// (rune-safe), appending a truncation marker.
func capOutput(s string, limit int) string {
	s = strings.TrimRight(s, " \t\r\n")
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := s[:limit]
	// Back off until we sit on a UTF-8 rune boundary.
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n...[truncated]"
}

// tokenizeArgs splits raw arguments the way a minimal POSIX shell would:
// whitespace-separated, single quotes fully literal, double quotes allowing
// \" and \\ escapes, no $ expansion. Unmatched quotes are tolerated (the
// token ends at end of input).
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
