package agent

import (
	"strings"
	"testing"
)

// =============================================================================
// Tests for basic verbose formatting
// =============================================================================

func TestFormatBasicVerboseSubagentContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty content",
			input:    "",
			expected: "",
		},
		{
			name:     "content without tool notifications",
			input:    "STATUS: completed\nSUMMARY: Task done\nDETAILS:\nAll files created successfully",
			expected: "STATUS: completed\nSUMMARY: Task done\nDETAILS:\nAll files created successfully",
		},
		{
			name: "removes verbose tool notification header but keeps command output",
			input: `🛠️ Exec: push git changes (in ~/.lele/workspace/lele)
cd ~/.lele/workspace/lele && git push
📤 Output:
To github.com:xilistudios/lele
   1234567..89abcdef  main -> main

STATUS: completed`,
			// The tool notification header (🛠️) and output header (📤) are removed,
			// but the actual command and output are kept (they might be useful context)
			expected: "cd ~/.lele/workspace/lele && git push\nTo github.com:xilistudios/lele\n   1234567..89abcdef  main -> main\n\nSTATUS: completed",
		},
		{
			name: "preserves important status lines",
			input: `🛠️ Read: config.json (in ~/.config)

→ Reading file...

STATUS: needs_context
CONTEXT_NEEDED: User confirmation required for deployment

DETAILS:
Need user to confirm before proceeding with the deployment.`,
			expected: "STATUS: needs_context\nCONTEXT_NEEDED: User confirmation required for deployment\n\nDETAILS:\nNeed user to confirm before proceeding with the deployment.",
		},
		{
			name:     "truncates individual lines longer than 300 chars in details",
			input:    "STATUS: completed\nDETAILS:\n" + strings.Repeat("x", 400),
			expected: "STATUS: completed\nDETAILS:\n" + strings.Repeat("x", 297) + "...",
		},
		{
			name:     "does not truncate lines at exactly 300 chars",
			input:    "STATUS: completed\nDETAILS:\n" + strings.Repeat("x", 300),
			expected: "STATUS: completed\nDETAILS:\n" + strings.Repeat("x", 300),
		},
		{
			name: "handles multi-line tool output with indentation",
			input: `🛠️ Exec: list files
ls -la /home/user

drwxr-xr-x  12 user  staff   384 Mar 21 10:00 .
drwxr-xr-x   9 user  staff   306 Mar 21 09:00 ..

STATUS: completed`,
			expected: "ls -la /home/user\n\ndrwxr-xr-x  12 user  staff   384 Mar 21 10:00 .\ndrwxr-xr-x   9 user  staff   306 Mar 21 09:00 ..\n\nSTATUS: completed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatBasicVerboseSubagentContent(tt.input)
			if result != tt.expected {
				t.Errorf("formatBasicVerboseSubagentContent()\ninput:\n%s\n\ngot:\n%s\n\nwant:\n%s", tt.input, result, tt.expected)
			}
		})
	}
}
