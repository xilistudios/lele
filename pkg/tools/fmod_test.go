package tools

import (
	"testing"
)

func TestDetectEncoding(t *testing.T) {
	tests := []struct {
		name            string
		input          []byte
		wantEncoding   string
		wantContent    string
	}{
		{
			name:          "UTF-8 without BOM",
			input:        []byte("Hello World"),
			wantEncoding: "UTF-8",
			wantContent:  "Hello World",
		},
		{
			name:          "UTF-8 with BOM",
			input:        append([]byte{0xEF, 0xBB, 0xBF}, []byte("Hello World")...),
			wantEncoding: "UTF-8-BOM",
			wantContent:  "Hello World",
		},
		{
			name:          "UTF-16 BE",
			input:        append([]byte{0xFE, 0xFF}, []byte{0x00, 0x48, 0x00, 0x65}...), // "He"
			wantEncoding: "UTF-16BE",
			wantContent:  string([]byte{0x00, 0x48, 0x00, 0x65}),
		},
		{
			name:          "UTF-16 LE",
			input:        append([]byte{0xFF, 0xFE}, []byte{0x48, 0x00, 0x65, 0x00}...), // "He"
			wantEncoding: "UTF-16LE",
			wantContent:  string([]byte{0x48, 0x00, 0x65, 0x00}),
		},
		{
			name:          "Empty input",
			input:        []byte{},
			wantEncoding: "UTF-8",
			wantContent:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoding, content := DetectEncoding(tt.input)
			if encoding != tt.wantEncoding {
				t.Errorf("DetectEncoding() got encoding = %v, want %v", encoding, tt.wantEncoding)
			}
			if string(content) != tt.wantContent {
				t.Errorf("DetectEncoding() got content = %v, want %v", string(content), tt.wantContent)
			}
		})
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello  world", "hello world"},
		{"hello\t\tworld", "hello world"},
		{"hello\n\nworld", "hello world"},
		{"hello   \t  \n  world", "hello world"},
		{"hello world", "hello world"},
		{"", ""},
	}

	for _, tt := range tests {
		result := normalizeWhitespace(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeWhitespace(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestExactMatchStrategy(t *testing.T) {
	strategy := &ExactMatchStrategy{}
	
	tests := []struct {
		content  string
		oldStr   string
		expected int // number of expected matches
	}{
		{"hello world", "hello", 1},
		{"hello hello world", "hello", 2},
		{"hello world", "foo", 0},
		{"", "foo", 0},
		{"foo", "", 0}, // empty string edge case
		{"hello\tworld", "hello", 1},
	}

	for _, tt := range tests {
		matches := strategy.FindMatches(tt.content, tt.oldStr)
		if len(matches) != tt.expected {
			t.Errorf("FindMatches(%q, %q) = %d matches, want %d", 
				tt.content, tt.oldStr, len(matches), tt.expected)
		}
	}
}

func TestWhitespaceTolerantStrategy(t *testing.T) {
	strategy := &WhitespaceTolerantStrategy{}
	
	tests := []struct {
		content  string
		oldStr   string
		expected int // number of expected matches
	}{
		{"hello world", "hello world", 1},
		{"hello   world", "hello world", 1}, // extra spaces in content
		{"hello\tworld", "hello world", 1},  // tab vs space
		{"hello world", "hello   world", 1}, // extra spaces in pattern
		{"hello\n\nworld", "hello world", 1}, // newlines in content
		{"hello world foo", "hello world", 1},
		{"abcde", "xyz", 0},
	}

	for _, tt := range tests {
		matches := strategy.FindMatches(tt.content, tt.oldStr)
		if len(matches) != tt.expected {
			t.Errorf("FindMatches(%q, %q) = %d matches, want %d", 
				tt.content, tt.oldStr, len(matches), tt.expected)
		}
	}
}

func TestRegexMatchStrategy(t *testing.T) {
	tests := []struct {
		name     string
		flags    string
		content  string
		pattern  string
		expected int
	}{
		{"simple match", "", "hello world", "hello", 1},
		{"case insensitive", "i", "HELLO world", "hello", 1},
		{"global match", "g", "hello hello world", "hello", 2},
		{"global + case", "gi", "HELLO hello world", "hello", 2},
		{"no match", "", "hello world", "foo", 0},
		{"pattern with special chars", "", "func main() {", `func\s+main\(\)`, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := &RegexMatchStrategy{Flags: tt.flags}
			matches := strategy.FindMatches(tt.content, tt.pattern)
			if len(matches) != tt.expected {
				t.Errorf("FindMatches(%q, %q, flags=%q) = %d matches, want %d",
					tt.content, tt.pattern, tt.flags, len(matches), tt.expected)
			}
		})
	}
}

func TestReplaceRange(t *testing.T) {
	tests := []struct {
		content  string
		match    Match
		newText  string
		expected string
	}{
		{
			"hello world",
			Match{Start: 0, End: 5},
			"hi",
			"hi world",
		},
		{
			"hello world",
			Match{Start: 6, End: 11},
			"universe",
			"hello universe",
		},
		{
			"hello world foo",
			Match{Start: 6, End: 11},
			"bar",
			"hello bar foo",
		},
	}

	for _, tt := range tests {
		result := ReplaceRange(tt.content, tt.match, tt.newText)
		if result != tt.expected {
			t.Errorf("ReplaceRange(%q, %v, %q) = %q, want %q",
				tt.content, tt.match, tt.newText, result, tt.expected)
		}
	}
}

func TestDetectOverlaps(t *testing.T) {
	tests := []struct {
		name     string
		matches  []Match
		expected bool
	}{
		{
			"no overlap",
			[]Match{{Start: 0, End: 5}, {Start: 6, End: 10}},
			false,
		},
		{
			"overlap",
			[]Match{{Start: 0, End: 5}, {Start: 3, End: 8}},
			true,
		},
		{
			"adjacent (no overlap)",
			[]Match{{Start: 0, End: 5}, {Start: 5, End: 10}},
			false,
		},
		{
			"single match",
			[]Match{{Start: 0, End: 5}},
			false,
		},
		{
			"empty",
			[]Match{},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectOverlaps(tt.matches)
			if result != tt.expected {
				t.Errorf("DetectOverlaps(%v) = %v, want %v", tt.matches, result, tt.expected)
			}
		})
	}
}

func TestGetBOM(t *testing.T) {
	tests := []struct {
		encoding string
		expected []byte
	}{
		{"UTF-8", nil},                    // UTF-8 without BOM should return nil
		{"utf-8", nil},                    // UTF-8 lowercase without BOM should return nil
		{"UTF-8-BOM", []byte{0xEF, 0xBB, 0xBF}}, // UTF-8-BOM should return BOM
		{"UTF-8-bom", []byte{0xEF, 0xBB, 0xBF}}, // UTF-8-bom lowercase should return BOM
		{"UTF-16BE", []byte{0xFE, 0xFF}},
		{"UTF-16LE", []byte{0xFF, 0xFE}},
		{"UTF-32BE", []byte{0x00, 0x00, 0xFE, 0xFF}},
		{"UTF-32LE", []byte{0xFF, 0xFE, 0x00, 0x00}},
		{"UNKNOWN", nil},
	}

	for _, tt := range tests {
		result := GetBOM(tt.encoding)
		if len(result) != len(tt.expected) {
			t.Errorf("GetBOM(%q) length = %d, want %d", tt.encoding, len(result), len(tt.expected))
			continue
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Errorf("GetBOM(%q)[%d] = %x, want %x", tt.encoding, i, result[i], tt.expected[i])
				break
			}
		}
	}
}
