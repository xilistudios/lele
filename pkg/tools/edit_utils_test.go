package tools

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestDetectEncoding covers all BOM detection branches.
func TestDetectEncoding(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		wantEnc  string
		wantBody []byte
	}{
		{name: "utf8", input: []byte("hello"), wantEnc: "UTF-8", wantBody: []byte("hello")},
		{name: "utf8-bom", input: []byte{0xEF, 0xBB, 0xBF, 'a'}, wantEnc: "UTF-8-BOM", wantBody: []byte{'a'}},
		{name: "utf16be", input: []byte{0xFE, 0xFF, 0x00, 'a'}, wantEnc: "UTF-16BE", wantBody: []byte{0x00, 'a'}},
		{name: "utf16le", input: []byte{0xFF, 0xFE, 'a', 0x00}, wantEnc: "UTF-16LE", wantBody: []byte{'a', 0x00}},
		{name: "utf32be", input: []byte{0x00, 0x00, 0xFE, 0xFF, 'a'}, wantEnc: "UTF-32BE", wantBody: []byte{'a'}},
		{name: "utf32le", input: []byte{0xFF, 0xFE, 0x00, 0x00, 'a'}, wantEnc: "UTF-32LE", wantBody: []byte{'a'}},
		{name: "empty", input: []byte{}, wantEnc: "UTF-8", wantBody: []byte{}},
		{name: "short", input: []byte{0xEF}, wantEnc: "UTF-8", wantBody: []byte{0xEF}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc, body := DetectEncoding(tc.input)
			if enc != tc.wantEnc {
				t.Fatalf("encoding = %q, want %q", enc, tc.wantEnc)
			}
			if string(body) != string(tc.wantBody) {
				t.Fatalf("body = %q, want %q", string(body), string(tc.wantBody))
			}
		})
	}
}

// TestGetBOM covers the BOM bytes for each encoding.
func TestGetBOM(t *testing.T) {
	tests := []struct {
		encoding string
		want     []byte
	}{
		{"UTF-8-BOM", []byte{0xEF, 0xBB, 0xBF}},
		{"utf-8-bom", []byte{0xEF, 0xBB, 0xBF}},
		{"UTF-16BE", []byte{0xFE, 0xFF}},
		{"UTF-16LE", []byte{0xFF, 0xFE}},
		{"UTF-32BE", []byte{0x00, 0x00, 0xFE, 0xFF}},
		{"UTF-32LE", []byte{0xFF, 0xFE, 0x00, 0x00}},
		{"UTF-8", nil},
		{"unknown", nil},
	}
	for _, tc := range tests {
		got := GetBOM(tc.encoding)
		if got == nil && tc.want != nil {
			t.Fatalf("GetBOM(%q) = nil, want %v", tc.encoding, tc.want)
		}
		if got != nil && tc.want == nil {
			t.Fatalf("GetBOM(%q) = %v, want nil", tc.encoding, got)
		}
		if got != nil && !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("GetBOM(%q) = %v, want %v", tc.encoding, got, tc.want)
		}
	}
}

// TestReadFileWithEncoding covers BOM stripping on read.
func TestReadFileWithEncoding(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	os.WriteFile(p, []byte{0xEF, 0xBB, 0xBF, 'h', 'i'}, 0644)

	content, encoding, err := ReadFileWithEncoding(p)
	if err != nil {
		t.Fatalf("ReadFileWithEncoding: %v", err)
	}
	if encoding != "UTF-8-BOM" {
		t.Fatalf("encoding = %q", encoding)
	}
	if content != "hi" {
		t.Fatalf("content = %q", content)
	}
}

// TestWriteFileWithEncoding verifies BOM is prepended on write.
func TestWriteFileWithEncoding(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := WriteFileWithEncoding(p, "hi", "UTF-8-BOM"); err != nil {
		t.Fatalf("WriteFileWithEncoding: %v", err)
	}
	data, _ := os.ReadFile(p)
	if data[0] != 0xEF || data[1] != 0xBB || data[2] != 0xBF {
		t.Fatalf("expected UTF-8 BOM, got %v", data[:3])
	}
	if string(data[3:]) != "hi" {
		t.Fatalf("content = %q", string(data[3:]))
	}
}

// TestExactMatchStrategy verifies literal matching.
func TestExactMatchStrategy(t *testing.T) {
	s := &ExactMatchStrategy{}
	matches := s.FindMatches("foo foo", "foo")
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(matches))
	}
	if matches[0].Start != 0 || matches[0].End != 3 {
		t.Fatalf("match0 = %d-%d", matches[0].Start, matches[0].End)
	}
	if matches[1].Start != 4 || matches[1].End != 7 {
		t.Fatalf("match1 = %d-%d", matches[1].Start, matches[1].End)
	}

	// Empty pattern returns no matches.
	if m := s.FindMatches("abc", ""); len(m) != 0 {
		t.Fatalf("empty pattern got %d matches", len(m))
	}
	// No match.
	if m := s.FindMatches("abc", "zzz"); len(m) != 0 {
		t.Fatalf("no-match got %d matches", len(m))
	}
}

// TestRegexMatchStrategy verifies regex matching and flags.
func TestRegexMatchStrategy(t *testing.T) {
	s := &RegexMatchStrategy{}
	matches := s.FindMatches("a1b22", "[0-9]+")
	if len(matches) != 1 {
		t.Fatalf("non-global matches = %d, want 1", len(matches))
	}

	g := &RegexMatchStrategy{Flags: "g"}
	gm := g.FindMatches("a1b22", "[0-9]+")
	if len(gm) != 2 {
		t.Fatalf("global matches = %d, want 2", len(gm))
	}

	ci := &RegexMatchStrategy{Flags: "i"}
	cm := ci.FindMatches("AbC", "abc")
	if len(cm) != 1 {
		t.Fatalf("case-insensitive matches = %d, want 1", len(cm))
	}

	// Invalid pattern returns nil.
	if m := s.FindMatches("abc", "["); m != nil {
		t.Fatalf("invalid pattern should return nil, got %v", m)
	}

	// No match.
	if m := s.FindMatches("abc", "zzz"); len(m) != 0 {
		t.Fatalf("no-match got %d", len(m))
	}
}

// TestWhitespaceTolerantStrategy verifies normalization before matching.
func TestWhitespaceTolerantStrategy(t *testing.T) {
	s := &WhitespaceTolerantStrategy{}
	matches := s.FindMatches("hello   world  foo", "hello world")
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	if matches[0].Start != 0 || matches[0].End != 13 {
		t.Fatalf("match = %d-%d, want 0-13", matches[0].Start, matches[0].End)
	}
}

// TestNormalizeWhitespace directly tests the normalization helper.
func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"a  b   c", "a b c"},
		{"\ta\t b", " a b"},
		{"  ", " "},
		{"", ""},
		{"single", "single"},
	}
	for _, tc := range tests {
		if got := normalizeWhitespace(tc.in); got != tc.want {
			t.Errorf("normalizeWhitespace(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestMapNormalizedToOriginal verifies position remapping.
func TestMapNormalizedToOriginal(t *testing.T) {
	original := "hello   world"
	// normalized: "hello world" (indices: h=0..., space at 5, w at 6)
	if got := mapNormalizedToOriginal(original, 0); got != 0 {
		t.Fatalf("pos0 = %d", got)
	}
	// normalized position 5 maps to original index 5 (end of "hello").
	if got := mapNormalizedToOriginal(original, 5); got != 5 {
		t.Fatalf("pos5 = %d, want 5", got)
	}
	// normalized position 6 maps to 'w' in original (index 8).
	if got := mapNormalizedToOriginal(original, 6); got != 8 {
		t.Fatalf("pos6 = %d, want 8", got)
	}
	// beyond end returns original length.
	if got := mapNormalizedToOriginal(original, 100); got != len(original) {
		t.Fatalf("pos100 = %d, want %d", got, len(original))
	}
	// non-positive returns 0.
	if got := mapNormalizedToOriginal(original, -1); got != 0 {
		t.Fatalf("pos-1 = %d", got)
	}
}

// TestDetectOverlaps verifies overlap detection helper.
func TestDetectOverlaps(t *testing.T) {
	if DetectOverlaps([]Match{{Start: 0, End: 3}, {Start: 2, End: 5}}) != true {
		t.Fatal("expected overlap detected")
	}
	if DetectOverlaps([]Match{{Start: 0, End: 3}, {Start: 3, End: 5}}) != false {
		t.Fatal("expected no overlap for adjacent matches")
	}
	if DetectOverlaps([]Match{{Start: 1, End: 2}, {Start: 0, End: 5}}) != true {
		t.Fatal("expected overlap for nested matches")
	}
	if DetectOverlaps(nil) != false {
		t.Fatal("expected false for empty")
	}
}

// TestApplyReplacements_OverlapDirect verifies detection error in ApplyReplacements.
func TestApplyReplacements_OverlapDirect(t *testing.T) {
	strategy := &ExactMatchStrategy{}
	_, err := ApplyReplacements("hello world", []ReplacementPair{
		{Old: "hello", New: "X"},
		{Old: "llo wo", New: "Y"},
	}, strategy)
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("err = %v, want overlap error", err)
	}
}

// TestApplyReplacements_Order verifies end-to-start application ordering.
func TestApplyReplacements_Order(t *testing.T) {
	strategy := &ExactMatchStrategy{}
	out, err := ApplyReplacements("aa bb", []ReplacementPair{
		{Old: "aa", New: "1"},
		{Old: "bb", New: "2"},
	}, strategy)
	if err != nil {
		t.Fatalf("ApplyReplacements: %v", err)
	}
	if out != "1 2" {
		t.Fatalf("out = %q", out)
	}
}

// TestReadLines verifies line-range reading.
func TestReadLines(t *testing.T) {
	lines, err := ReadLines("a\nb\nc\n", 2, 3)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if len(lines) != 2 || lines[0].Number != 2 || lines[0].Text != "b" {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[1].Number != 3 || lines[1].Text != "c" {
		t.Fatalf("lines[1] = %+v", lines[1])
	}
}

// TestReadLines_InvalidRange verifies from>to error.
func TestReadLines_InvalidRange(t *testing.T) {
	if _, err := ReadLines("a\nb\n", 5, 2); err == nil {
		t.Fatal("expected error for from>to")
	}
}

// TestReadLines_Clamps verifies clamping of out-of-range values.
func TestReadLines_Clamps(t *testing.T) {
	lines, err := ReadLines("a\nb\nc\n", -5, 100)
	if err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4 (trailing empty split element)", len(lines))
	}
}

// TestReplaceRangeDirect verifies string range replacement.
func TestReplaceRangeDirect(t *testing.T) {
	out := ReplaceRange("hello world", Match{Start: 6, End: 11}, "there")
	if out != "hello there" {
		t.Fatalf("out = %q", out)
	}
}