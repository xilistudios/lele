package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const longSuccessReport = `STATUS: completed
SUMMARY: Fixed the flaky upload tests and moved the retry logic into pkg/storage
DETAILS:
Changed three files. The whole package suite runs green now.

While reproducing the failure the fixture file was missing from the checkout,
so the first run could not reproduce the panic, and one test failed to compile
before the fix landed. An error occurred in the CI copy as well: the upload
helper panicked on a nil reader. All of it is resolved; the retries are bounded
and the helper returns a wrapped error instead.

Nothing is pending on this task and no further information is required. If the
retry budget should be configurable, please provide the intended default.`

func TestParseSubagentOutcome(t *testing.T) {
	tests := []struct {
		name               string
		raw                string
		wantStatus         string
		wantSummary        string
		wantDetailsSubstrs []string
		wantContextRequest string
	}{
		{
			name: "explicit completed with failure words in details",
			raw: "STATUS: completed\n" +
				"SUMMARY: Added rate limiting to the gateway\n" +
				"DETAILS:\n" +
				"The missing config key was added to config.example.yaml. A test\n" +
				"failed to compile before the fix and I could not reproduce the panic\n" +
				"until error handling was added. An error occurred in the smoke run.\n" +
				"Please provide feedback if the naming is wrong; the work is done.\n",
			wantStatus:         SubagentStatusCompleted,
			wantSummary:        "Added rate limiting to the gateway",
			wantContextRequest: "",
		},
		{
			name: "prose success mentioning past failures",
			raw: "Refactored the CSV importer and the suite is green.\n\n" +
				"The test failed to compile before the fix because a helper was missing,\n" +
				"and I could not reproduce initially until I set TMPDIR. Both are resolved.\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Refactored the CSV importer and the suite is green.",
		},
		{
			name: "details section is terminal for a quoted status line",
			raw: "STATUS: completed\n" +
				"SUMMARY: Documented the ordering bug\n" +
				"DETAILS:\n" +
				"The old parser flipped the outcome whenever a report quoted the\n" +
				"contract, for example a line like:\n" +
				"STATUS: not_done\n" +
				"or CONTEXT_NEEDED: something. That is fixed here.\n",
			wantStatus:         SubagentStatusCompleted,
			wantSummary:        "Documented the ordering bug",
			wantDetailsSubstrs: []string{"STATUS: not_done", "CONTEXT_NEEDED: something"},
		},
		{
			name:        "prose opening declares not_done",
			raw:         "I could not complete the task because the repo was not writable.\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "I could not complete the task because the repo was not writable.",
		},
		{
			name: "prose opening asks for context",
			raw: "I need more information about which migration to run.\n" +
				"The directory holds two candidate files and the task does not say which one is authoritative.\n",
			wantStatus:         SubagentStatusNeedsContext,
			wantContextRequest: "I need more information about which migration to run.",
		},
		{
			name: "explicit needs_context keeps context needed line",
			raw: "STATUS: needs_context\n" +
				"SUMMARY: Which file should I patch?\n" +
				"CONTEXT_NEEDED: Which file?\n" +
				"DETAILS:\n" +
				"The task names two candidate modules.\n",
			wantStatus:         SubagentStatusNeedsContext,
			wantSummary:        "Which file should I patch?",
			wantContextRequest: "Which file?",
		},
		{
			name:        "synthetic max iterations content",
			raw:         toolLoopMaxIterationsContent,
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "Maximum iterations reached without completing the task",
			wantDetailsSubstrs: []string{
				"The subagent ran out of iterations while still using tools.",
			},
		},
		{
			name:        "synthetic empty response content",
			raw:         toolLoopEmptyResponseContent,
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "The model returned empty responses repeatedly",
		},
		{
			name:               "fenced contract output",
			raw:                "```text\nSTATUS: completed\nSUMMARY: ok\nDETAILS:\nall good\n```",
			wantStatus:         SubagentStatusCompleted,
			wantSummary:        "ok",
			wantDetailsSubstrs: []string{"all good"},
		},
		{
			name:        "json inside markdown fence",
			raw:         "```json\n{\"status\":\"completed\",\"summary\":\"s\",\"details\":\"failed to do nothing\"}\n```",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "s",
		},
		{
			name:        "raw json object",
			raw:         "{\"status\":\"not_done\",\"summary\":\"no write access\",\"details\":\"missing binary\"}",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "no write access",
		},
		{
			name:        "long successful engineering report",
			raw:         longSuccessReport,
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Fixed the flaky upload tests and moved the retry logic into pkg/storage",
			wantDetailsSubstrs: []string{
				"the fixture file was missing",
				"failed to compile",
				"could not reproduce the panic",
			},
		},
		{
			name:        "empty output stays completed",
			raw:         "",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "",
		},
		{
			name: "prose opening is blocked",
			raw: "Blocked: the database port is not reachable from this sandbox.\n" +
				"I verified the firewall rules twice.\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "prose opening ran out of time",
			raw:        "I ran out of time before writing the migration.\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:               "prose opening is the needs_context keyword",
			raw:                "needs_context: the task refers to an attachment that is not here.\n",
			wantStatus:         SubagentStatusNeedsContext,
			wantContextRequest: "the task refers to an attachment that is not here.",
		},
		{
			name: "declaration beyond the opening window is ignored",
			raw: "Renamed the module and updated the callers.\n" +
				"Tests pass and the package builds.\n" +
				"The changelog entry is written.\n" +
				"The README section is updated.\n" +
				"Coverage numbers are recorded below.\n" +
				"I could not complete the deployment step, that one is on you.\n",
			wantStatus: SubagentStatusCompleted,
		},

		{
			name:        "prose about blocked jobs stays completed",
			raw:         "Blocked jobs are now retried three times before failing.\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Blocked jobs are now retried three times before failing.",
		},
		{
			name:        "prose about stuck uploads stays completed",
			raw:         "Stuck uploads are now surfaced in the UI and retried.\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Stuck uploads are now surfaced in the UI and retried.",
		},
		{
			name:        "prose about incomplete task handling stays completed",
			raw:         "Task incomplete handling was added to the API and tested.\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Task incomplete handling was added to the API and tested.",
		},
		{
			name:        "prose about not done items stays completed",
			raw:         "Not done items are now visible in the report.\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Not done items are now visible in the report.",
		},
		{
			name:               "headline plus prose about blocked jobs stays completed",
			raw:                "# Queue fix\n\nBlocked jobs are now retried three times.\n",
			wantStatus:         SubagentStatusCompleted,
			wantContextRequest: "",
		},
		{
			name:        "prose about needs-context inference stays completed",
			raw:         "Needs context inference is now part of the planner.\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Needs context inference is now part of the planner.",
		},

		{
			name:       "cannot complete without to",
			raw:        "I cannot complete the task because the repo was not writable.\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "contraction with unable to",
			raw:        "I'm unable to complete the task.\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "was not able to finish",
			raw:        "I was not able to finish.\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "inability admitted mid sentence",
			raw:        "I've done most of it but I could not complete the task.\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "decorated inability",
			raw:        "**I was unable to complete the task.**\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:               "decorated list bullet asks for context",
			raw:                "- I need more context about the target branch.\n",
			wantStatus:         SubagentStatusNeedsContext,
			wantContextRequest: "I need more context about the target branch.",
		},
		{
			name:       "decorated needs context heading",
			raw:        "**Needs context:** which of the two schemas is authoritative?\n",
			wantStatus: SubagentStatusNeedsContext,
		},

		{
			name:               "bold status field",
			raw:                "**STATUS: not_done**\nSUMMARY: no write access\nDETAILS:\nCould not write.\n",
			wantStatus:         SubagentStatusNotDone,
			wantSummary:        "no write access",
			wantDetailsSubstrs: []string{"Could not write."},
		},
		{
			name:        "heading status field",
			raw:         "## STATUS: not_done\nSUMMARY: no write access\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "no write access",
		},
		{
			name:               "list bullet status field",
			raw:                "- STATUS: needs_context\nCONTEXT_NEEDED: Which file?\n",
			wantStatus:         SubagentStatusNeedsContext,
			wantContextRequest: "Which file?",
		},
		{
			name:        "closing status never contradicts a header declaration",
			raw:         "STATUS: completed\nSUMMARY: ok\nDETAILS:\nbody\n\n**STATUS: not_done**",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "ok",
		},
		{
			name:        "closing status rescues a header without a declaration",
			raw:         "SUMMARY: half done\nDETAILS:\nbody\n\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "half done",
		},
		{
			name:        "fenced status line is a quote not a closing declaration",
			raw:         "SUMMARY: wrote the docs\nDETAILS:\nThe contract is:\n```\nSTATUS: not_done\n```\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "wrote the docs",
		},
		{
			name:        "status quoted inside details does not count",
			raw:         "STATUS: completed\nSUMMARY: ok\nDETAILS:\nline\nSTATUS: not_done\nmore body text here\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "ok",
		},
		{
			name:        "in progress declaration is not a success",
			raw:         "STATUS: in progress\nSUMMARY: half done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "half done",
		},
		{name: "partial declaration is not a success", raw: "STATUS: partial\n", wantStatus: SubagentStatusNotDone},
		{
			name:       "pending declaration is terminal, not a success",
			raw:        "STATUS: pending\n",
			wantStatus: SubagentStatusNotDone,
		},
		{name: "wip declaration is not a success", raw: "STATUS: wip\n", wantStatus: SubagentStatusNotDone},
		{
			name:       "in progress with a file-count clause is not a success",
			raw:        "STATUS: in progress (3/8 files done)\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "not_done with a file path clause is not a success",
			raw:        "STATUS: not_done - pkg/tools/registry.go unreadable\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "non-success enumeration is a declaration, not a legend",
			raw:        "STATUS: blocked/pending - the CI token is missing\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "non-success or-enumeration is a declaration",
			raw:        "STATUS: failed or needs_context\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:        "unrecognised status value falls through to the default",
			raw:         "STATUS: see the details below\nSUMMARY: half done\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "half done",
		},
		{
			name:       "out-of-vocabulary status value is classified",
			raw:        "STATUS: could not complete the task, CI is red\nSUMMARY: half done\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:               "out-of-vocabulary context request is classified",
			raw:                "STATUS: I need more context about the deploy target\n",
			wantStatus:         SubagentStatusNeedsContext,
			wantContextRequest: "I need more context about the deploy target",
			wantSummary:        "I need more context about the deploy target",
		},
		{
			name:       "out-of-vocabulary value is classified through decoration",
			raw:        "STATUS: ❌ \"could not complete\" the migration\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "qualified completion is a not_done declaration",
			raw:        "STATUS: almost done but one test is red\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:        "an echo cannot upgrade the outcome",
			raw:         "STATUS: not_done\nSUMMARY: parser half fixed\nSTATUS: completed\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "parser half fixed",
		},
		{
			name:        "fence with a punctuated info string still delimits",
			raw:         "SUMMARY: x\nDETAILS:\n```c#\nvar x = 1;\n```\n\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "x",
		},
		{
			name:        "tilde fence quotes its status",
			raw:         "SUMMARY: x\nDETAILS:\n~~~\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "x",
		},
		{
			name:       "an inferred status does not outrank a declaration",
			raw:        "STATUS: I need more context about the deploy target\nSUMMARY: s\nSTATUS: completed\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			name:       "an inferred status leaves the closing rule in play",
			raw:        "STATUS: I need more context about the deploy target\nSUMMARY: s\nDETAILS:\nbody\n\nSTATUS: completed\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			name:       "wrapper with a spaced info string is unwrapped",
			raw:        "```console title=session\nSTATUS: not_done\nSUMMARY: x\n```\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "tilde wrapper is unwrapped",
			raw:        "~~~\nSTATUS: not_done\nSUMMARY: x\n~~~\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "decorated fence opener is a fence",
			raw:        "- ```go\ncode\n```\n\nSTATUS: not_done\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:        "a decorated fence line inside a block is content",
			raw:         "SUMMARY: docs\nDETAILS:\nThe patch adds a fence opener:\n```diff\n+ ```go\n```\n\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "docs",
		},
		{
			name:       "blockquote wrapper is unwrapped",
			raw:        "> ```\n> STATUS: not_done\n> SUMMARY: x\n> ```\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "bullet-opened block is closed by a bullet marker",
			raw:        "- ```go\ncode\n- ```\n\nSTATUS: not_done\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "unclosed wrapper is unwrapped",
			raw:        "```text\nSTATUS: not_done\nSUMMARY: x\nDETAILS:\nbody\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:        "a leading code block is not a wrapper",
			raw:         "```\ncode\n```\n\nSUMMARY: x\nDETAILS:\nbody\n\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "x",
		},
		{
			name:       "a closed block at the start and a stray run at the end",
			raw:        "```\na\n```\n\nSTATUS: not_done\n\n```\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "a tilde line cannot close a backtick block",
			raw:        "SUMMARY: x\nDETAILS:\n```\n~~~\n```\n\nSTATUS: not_done\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "html comment sign-off does not defeat the closing status",
			raw:        "SUMMARY: x\nDETAILS:\nbody\n\nSTATUS: not_done\n\n<!-- end of report -->\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "emoji sign-off does not defeat the closing status",
			raw:        "SUMMARY: x\nDETAILS:\nbody\n\nSTATUS: not_done\n\n🦞\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "a pasted json example cannot override the declaration",
			raw:        "STATUS: not_done\nSUMMARY: x\nDETAILS:\nThe payload is:\n```json\n{\"status\":\"completed\",\"summary\":\"s\"}\n```\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:        "trailing tilde divider does not defeat the closing status",
			raw:         "SUMMARY: half done\nDETAILS:\nbody\n\nSTATUS: not_done\n\n~~~\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "half done",
		},
		{
			name:       "a declaration outranks a later inference",
			raw:        "STATUS: not_done\nSTATUS: I need more context about the schema\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "a completed declaration outranks a later inference",
			raw:        "STATUS: completed\nSUMMARY: s\nSTATUS: I need more context\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			name:        "a late downgrade is honoured",
			raw:         "STATUS: completed\nSUMMARY: wrote the migration\nSTATUS: not_done, the last test still fails\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "wrote the migration",
		},

		{
			name:        "T1 unclosed fence keeps the closing rule out",
			raw:         "SUMMARY: wrote the docs\nDETAILS:\nThe shape is:\n```\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "wrote the docs",
		},
		{
			name:        "T1 inline fenced status line is body text",
			raw:         "SUMMARY: x\nDETAILS:\n```STATUS: not_done```\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "x",
		},
		{
			name:        "inline code fence does not swallow the closing status",
			raw:         "SUMMARY: x\nDETAILS:\n```go vet ./...```\n\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "x",
		},
		{
			name:        "T2 unrecognised header value leaves the closing rule in play",
			raw:         "STATUS: see the details below\nSUMMARY: half done\nDETAILS:\nbody\n\nSTATUS: not_done",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "half done",
		},
		{
			name:       "M1 prose ending in a status line is inert",
			raw:        "The states are: queued, then\nStatus: blocked\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			name:       "M1 prose about blocked work ending in a status line is inert",
			raw:        "The task was blocked, retry\nStatus: blocked\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			name: "preamble before the contract keeps the declaration",
			raw: "Here is my report:\n" +
				"STATUS: not_done\n" +
				"SUMMARY: no write access\n" +
				"DETAILS:\nCould not write the files.\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "no write access",
		},
		{
			name:       "lower-case status after prose stays prose",
			raw:        "Here is my report:\nstatus: not_done\nSummary: done\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			name:       "M2 not_done with a trailing clause",
			raw:        "STATUS: not_done (blocked by CI)\n",
			wantStatus: SubagentStatusNotDone,
		},
		{name: "M2 blocked with a by clause", raw: "STATUS: blocked by the CI outage\n", wantStatus: SubagentStatusNotDone},
		{name: "M2 failed with an exit code", raw: "STATUS: failed (exit 1)\n", wantStatus: SubagentStatusNotDone},
		{name: "M2 not complete two-word value", raw: "STATUS: not complete\n", wantStatus: SubagentStatusNotDone},
		{name: "M2 completed with a trailing clause", raw: "STATUS: completed and all tests green\n", wantStatus: SubagentStatusCompleted},
		{
			name:       "M3 and-hyphen compound prose is inert",
			raw:        "Blocked-and-later-retried queue jobs now finish cleanly.\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			name:       "M3 by-hyphen compound prose is inert",
			raw:        "Blocked-by-design queue jobs now finish cleanly.\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			name:       "M3 blocked on a preposition declares",
			raw:        "Blocked on CI outage.\n",
			wantStatus: SubagentStatusNotDone,
		},
		{name: "M3 stuck on a preposition declares", raw: "Stuck on circular import.\n", wantStatus: SubagentStatusNotDone},
		{
			name:        "M4 fenced contract template is a preamble",
			raw:         "Here is the shape I will report:\n```text\nSTATUS: not_done\n```\nI fixed the parser and the suite is green.\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Here is the shape I will report:",
		},
		{
			name:        "M4 header after a quoted contract still parses",
			raw:         "```text\nSTATUS: not_done\n```\n\nSTATUS: completed\nSUMMARY: ok\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "ok",
		},
		{
			name:        "m1 trailing divider does not defeat the closing status",
			raw:         "SUMMARY: half done\nDETAILS:\nbody\n\nSTATUS: not_done\n\n---\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "half done",
		},
		{
			name:       "window fifth line declares",
			raw:        "# Review report\n\nScope: pkg tools.\nFiles read: 3.\nI could not complete the task: one file is unreadable.\n",
			wantStatus: SubagentStatusNotDone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSubagentOutcome(tt.raw)

			if got.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q (output: %q)", got.Status, tt.wantStatus, tt.raw)
			}
			if tt.wantSummary != "" && got.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", got.Summary, tt.wantSummary)
			}
			if tt.wantContextRequest != "" && got.ContextRequest != tt.wantContextRequest {
				t.Errorf("ContextRequest = %q, want %q", got.ContextRequest, tt.wantContextRequest)
			}
			if tt.wantStatus != SubagentStatusNeedsContext && got.ContextRequest != "" && tt.wantContextRequest == "" {
				t.Errorf("ContextRequest = %q, want empty for status %q", got.ContextRequest, tt.wantStatus)
			}
			for _, want := range tt.wantDetailsSubstrs {
				if !strings.Contains(got.Details, want) {
					t.Errorf("Details = %q, want to contain %q", got.Details, want)
				}
			}
			if tt.raw != "" && got.Details == "" {
				t.Error("Details must never be empty for non-empty output")
			}
		})
	}
}

func TestParseSubagentOutcomeCoverageGaps(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantStatus  string
		wantSummary string
	}{
		{
			name:       "status quoted mid body is inert",
			raw:        "SUMMARY: x\nDETAILS:\nbody\nSTATUS: not_done\nmore body\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			name:       "vocabulary legend is not a declaration",
			raw:        "STATUS: completed | not_done | needs_context\nDETAILS:\nI could not complete the task.\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "status value keeps its clause after the noise pass",
			raw:        "STATUS: almost finished (the CLI flag is unimplemented)\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "ordered list decoration",
			raw:        "1. STATUS: not_done\n2. SUMMARY: blocked on CI\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:        "progress line does not end the header",
			raw:         "STATUS: completed\nPROGRESS: 3 of 5 files\nSummary: s\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "s",
		},
	}
	for _, tc := range cases {
		got := parseSubagentOutcome(tc.raw)
		if got.Status != tc.wantStatus {
			t.Errorf("%s: Status = %q, want %q (raw %q)", tc.name, got.Status, tc.wantStatus, tc.raw)
		}
		if tc.wantSummary != "" && got.Summary != tc.wantSummary {
			t.Errorf("%s: Summary = %q, want %q", tc.name, got.Summary, tc.wantSummary)
		}
	}

	if got := parseSubagentOutcome("- implemented the rate limiter and tests\n").Summary; got != "implemented the rate limiter and tests" {
		t.Errorf("fallback Summary = %q, want the bullet stripped", got)
	}

	jsonOutcome := parseSubagentOutcome(`{"status":"needs_context","details":"STATUS: I need more context about X"}`)
	if jsonOutcome.Summary != "I need more context about X" {
		t.Errorf("json Summary = %q, want %q", jsonOutcome.Summary, "I need more context about X")
	}
	if got := parseSubagentOutcome("````\nSTATUS: completed\nSUMMARY: s\nDETAILS:\nbody\n````").Details; got != "body" {
		t.Errorf("four-tick wrapper Details = %q, want %q", got, "body")
	}
}

func TestParseSubagentOutcomeExplicitStatusIsAuthoritative(t *testing.T) {
	declared := []struct {
		token string
		want  string
	}{
		{"completed", SubagentStatusCompleted},
		{"complete", SubagentStatusCompleted},
		{"done", SubagentStatusCompleted},
		{"success", SubagentStatusCompleted},
		{"not_done", SubagentStatusNotDone},
		{"failed", SubagentStatusNotDone},
		{"blocked", SubagentStatusNotDone},
		{"incomplete", SubagentStatusNotDone},
		{"needs_context", SubagentStatusNeedsContext},
		{"need_context", SubagentStatusNeedsContext},
		{"waiting", SubagentStatusNeedsContext},
		{"cancelled", SubagentStatusCancelled},
		{"canceled", SubagentStatusCancelled},
	}
	bodies := []string{
		"",
		"Nothing else to add.",
		"The manifest was missing, one test failed to compile and I could not run the suite.",
		"I need more information and cannot proceed without it, please provide the credentials.",
		"Task not done; an error occurred while writing, I was not able to finish.",
		"STATUS: not_done\nCONTEXT_NEEDED: quoted inside the body",
	}

	for _, tc := range declared {
		t.Run("declared_"+tc.token, func(t *testing.T) {
			decl, want := tc.token, tc.want

			for i, body := range bodies {
				raw := "STATUS: " + decl + "\nSUMMARY: outcome under test\nDETAILS:\n" + body
				if got := parseSubagentOutcome(raw).Status; got != want {
					t.Errorf("text body %d: Status = %q, want %q", i, got, want)
				}

				fenced := "```text\nSTATUS: " + decl + "\nSUMMARY: outcome under test\nDETAILS:\n" + body + "\n```"
				if got := parseSubagentOutcome(fenced).Status; got != want {
					t.Errorf("fenced body %d: Status = %q, want %q", i, got, want)
				}
			}

			jsonRaw := `{"status":"` + decl + `","summary":"outcome under test","details":` +
				`"The manifest was missing, one test failed to compile and I could not run the suite."}`
			if got := parseSubagentOutcome(jsonRaw).Status; got != want {
				t.Errorf("json: Status = %q, want %q (body is inert in the json form)", got, want)
			}
		})
	}
}

func TestNormalizeSubagentStatus(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "completed", value: "completed", want: SubagentStatusCompleted},
		{name: "complete", value: "complete", want: SubagentStatusCompleted},
		{name: "done", value: "done", want: SubagentStatusCompleted},
		{name: "finished", value: "finished", want: SubagentStatusCompleted},
		{name: "success", value: "success", want: SubagentStatusCompleted},
		{name: "succeeded", value: "Succeeded", want: SubagentStatusCompleted},
		{name: "task done spaced", value: "  Task-Done ", want: SubagentStatusCompleted},
		{name: "not_done", value: "not_done", want: SubagentStatusNotDone},
		{name: "not-done", value: "NOT-DONE", want: SubagentStatusNotDone},
		{name: "failed", value: "failed", want: SubagentStatusNotDone},
		{name: "blocked", value: "blocked", want: SubagentStatusNotDone},
		{name: "stuck", value: "stuck", want: SubagentStatusNotDone},
		{name: "incomplete", value: "incomplete", want: SubagentStatusNotDone},
		{name: "needs_context", value: "needs_context", want: SubagentStatusNeedsContext},
		{name: "context needed", value: "Context Needed", want: SubagentStatusNeedsContext},
		{name: "waiting", value: "waiting", want: SubagentStatusNeedsContext},
		{name: "paused", value: "paused", want: SubagentStatusNeedsContext},
		{name: "cancelled", value: "cancelled", want: SubagentStatusCancelled},
		{name: "canceled", value: "canceled", want: SubagentStatusCancelled},
		{name: "empty falls back to completed", value: "", want: SubagentStatusCompleted},
		{name: "garbage falls back to completed", value: "🤷 probably fine", want: SubagentStatusCompleted},
		{name: "bold not_done", value: "**not_done**", want: SubagentStatusNotDone},
		{name: "sentence with space and period", value: "not done.", want: SubagentStatusNotDone},
		{name: "inline code completed", value: "`completed`", want: SubagentStatusCompleted},
		{name: "emoji not_done", value: "❌ not_done", want: SubagentStatusNotDone},
		{name: "emoji completed", value: "✅ done", want: SubagentStatusCompleted},
		{name: "emoji bold not_done", value: "🦞 **not_done**", want: SubagentStatusNotDone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeSubagentStatus(tt.value); got != tt.want {
				t.Errorf("normalizeSubagentStatus(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestSubagentStatusFromToken(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		want   string
		wantOK bool
	}{
		{name: "bare", value: "not_done", want: SubagentStatusNotDone, wantOK: true},
		{name: "decorated", value: " **not_done** ", want: SubagentStatusNotDone, wantOK: true},
		{name: "punctuated", value: "completed.", want: SubagentStatusCompleted, wantOK: true},
		{name: "spaced", value: "not done", want: SubagentStatusNotDone, wantOK: true},
		{name: "cancelled", value: "canceled", want: SubagentStatusCancelled, wantOK: true},
		{name: "emoji token", value: "❌ not_done", want: SubagentStatusNotDone, wantOK: true},
		{name: "partial is unfinished", value: "partial", want: SubagentStatusNotDone, wantOK: true},
		{name: "in progress is unfinished", value: "in_progress", want: SubagentStatusNotDone, wantOK: true},
		{name: "in progress spaced is unfinished", value: "in progress", want: SubagentStatusNotDone, wantOK: true},
		{name: "partially done is unfinished", value: "partially_done", want: SubagentStatusNotDone, wantOK: true},
		{name: "partially complete is unfinished", value: "partially complete", want: SubagentStatusNotDone, wantOK: true},
		{name: "not started is unfinished", value: "not_started", want: SubagentStatusNotDone, wantOK: true},
		{name: "pending is terminal", value: "pending", want: SubagentStatusNotDone, wantOK: true},
		{name: "wip is unfinished", value: "wip", want: SubagentStatusNotDone, wantOK: true},
		{
			name:   "file-count clause is a declaration",
			value:  "in progress (3/8 files done)",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			name:   "file count after an underscored token",
			value:  "not_done (2/7 files)",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			name:   "file path clause is a declaration",
			value:  "not_done - pkg/tools/registry.go unreadable",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			name:   "synonym alternation is a declaration",
			value:  "blocked/failed",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			name:   "non-success enumeration keeps its declaration",
			value:  "blocked/pending - the CI token is missing",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			name:   "non-success or enumeration keeps its declaration",
			value:  "failed or needs_context",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			name:   "two non-success statuses enumerated",
			value:  "not_done/needs_context",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			name:   "needs_context enumeration stays needs_context",
			value:  "waiting/blocked",
			want:   SubagentStatusNeedsContext,
			wantOK: true,
		},
		{
			name:   "in progress with a pending tail",
			value:  "in progress/pending",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			name:   "not_done with a partially-done tail",
			value:  "not_done or partially done",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{name: "unrecognised prose", value: "see the details below", want: "", wantOK: false},
		{name: "empty", value: "", want: "", wantOK: false},
		{name: "three-way legend", value: "completed | not_done | needs_context", want: "", wantOK: false},
		{name: "two-way legend", value: "completed/not_done", want: "", wantOK: false},
		{name: "two-way or legend", value: "completed or not_done", want: "", wantOK: false},
		{name: "reversed legend", value: "not_done | completed", want: "", wantOK: false},
		{name: "upper-case legend", value: "COMPLETED | NOT_DONE", want: "", wantOK: false},
		{name: "decorated legend", value: "**completed | not_done**", want: "", wantOK: false},
		{name: "trailing clause", value: "not_done (blocked by CI)", want: SubagentStatusNotDone, wantOK: true},
		{
			name:   "two-word token with clause",
			value:  "not done (blocked by CI)",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			name:   "clause without vocabulary head",
			value:  "almost done but one test is red",
			want:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := subagentStatusFromToken(tt.value)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("subagentStatusFromToken(%q) = (%q, %v), want (%q, %v)",
					tt.value, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestClassifySubagentOpening(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "need more context", text: "I need more context to continue.", want: SubagentStatusNeedsContext},
		{name: "requires additional info", text: "Requires additional info about the target host.", want: SubagentStatusNeedsContext},
		{name: "need details", text: "Need details on the expected format.", want: SubagentStatusNeedsContext},
		{name: "cannot proceed without", text: "I cannot proceed without the API key.", want: SubagentStatusNeedsContext},
		{name: "needs_context keyword", text: "needs_context: the bucket name is ambiguous", want: SubagentStatusNeedsContext},
		{name: "needs_context alone", text: "needs_context", want: SubagentStatusNeedsContext},
		{name: "unable to complete", text: "I was unable to complete the migration.", want: SubagentStatusNotDone},
		{name: "cannot complete", text: "I cannot complete the task.", want: SubagentStatusNotDone},
		{name: "was not able to finish", text: "I was not able to finish.", want: SubagentStatusNotDone},
		{name: "did not finish", text: "I did not finish the second file.", want: SubagentStatusNotDone},
		{name: "task incomplete", text: "Task is incomplete: two endpoints remain.", want: SubagentStatusNotDone},
		{name: "not done", text: "Not done: the build is still red.", want: SubagentStatusNotDone},
		{name: "blocked", text: "Blocked: a locked index file.", want: SubagentStatusNotDone},
		{name: "got stuck", text: "I got stuck: the circular import.", want: SubagentStatusNotDone},
		{name: "ran out of iterations", text: "I ran out of iterations mid-refactor.", want: SubagentStatusNotDone},
		{name: "admitted mid sentence", text: "I refactored the parser, but I could not complete the task.", want: SubagentStatusNotDone},
		{name: "plain success", text: "Implemented the endpoint and added tests.", want: ""},
		{name: "past tense mentions", text: "The test failed to compile before the fix.", want: ""},
		{name: "unrelated could not", text: "I could not find a better name for it.", want: ""},
		{name: "word missing in body", text: "Renamed the field so the missing case is covered.", want: ""},
		{name: "blocked jobs prose", text: "Blocked jobs are now retried three times before failing.", want: ""},
		{name: "stuck uploads prose", text: "Stuck uploads are now surfaced in the UI and retried.", want: ""},
		{name: "task incomplete prose", text: "Task incomplete handling was added to the API and tested.", want: ""},
		{name: "not done items prose", text: "Not done items are now visible in the report.", want: ""},
		{name: "blocked on preposition", text: "Blocked on CI outage.", want: SubagentStatusNotDone},
		{name: "stuck on preposition", text: "Stuck on circular import.", want: SubagentStatusNotDone},
		{name: "blocked with clause", text: "Blocked by a locked index file.", want: SubagentStatusNotDone},
		{name: "and-hyphen compound prose", text: "Blocked-and-later-retried queue jobs finish cleanly.", want: ""},
		{name: "by-hyphen compound prose", text: "Blocked-by-design queue jobs finish cleanly.", want: ""},
		{name: "needs context inference prose", text: "Needs context inference is now part of the planner.", want: ""},
		{name: "almost done", text: "Almost done.", want: SubagentStatusNotDone},
		{name: "almost done clause", text: "Almost done but one test is red.", want: SubagentStatusNotDone},
		{name: "partially complete colon", text: "partially complete: the CLI flag is unimplemented", want: SubagentStatusNotDone},
		{name: "mostly complete", text: "Mostly complete, one endpoint left.", want: SubagentStatusNotDone},
		{name: "partially completed prose", text: "Partially completed orders are now resumed on restart.", want: ""},
		{name: "mostly done prose", text: "Mostly done items are surfaced in the queue view now.", want: ""},
		{name: "nearly completed prose", text: "Nearly completed batches are retried automatically.", want: ""},
		{name: "empty text", text: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := subagentFallbackOpeningLines(tt.text, subagentFallbackMaxLines, subagentFallbackMaxChars)
			if got := classifySubagentOpening(lines); got != tt.want {
				t.Errorf("classifySubagentOpening(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestClassifySubagentOpeningEarliestLineWins(t *testing.T) {
	text := "I could not complete the task.\nI need more information about the target host.\n"
	lines := subagentFallbackOpeningLines(text, subagentFallbackMaxLines, subagentFallbackMaxChars)
	if got := classifySubagentOpening(lines); got != SubagentStatusNotDone {
		t.Errorf("classifySubagentOpening = %q, want %q (the first line declares)", got, SubagentStatusNotDone)
	}

	text = "I need more information about the target host.\nI could not complete the task.\n"
	lines = subagentFallbackOpeningLines(text, subagentFallbackMaxLines, subagentFallbackMaxChars)
	if got := classifySubagentOpening(lines); got != SubagentStatusNeedsContext {
		t.Errorf("classifySubagentOpening = %q, want %q", got, SubagentStatusNeedsContext)
	}

	text = "I need more context; I cannot complete the task.\n"
	lines = subagentFallbackOpeningLines(text, subagentFallbackMaxLines, subagentFallbackMaxChars)
	if got := classifySubagentOpening(lines); got != SubagentStatusNeedsContext {
		t.Errorf("classifySubagentOpening = %q, want %q (needs_context first within a line)", got, SubagentStatusNeedsContext)
	}
}

func TestSubagentFallbackOpeningLinesWindow(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "skips blank lines", text: "\n  first  \n\n\nsecond\n", want: []string{"first", "second"}},
		{name: "caps line count", text: "a\nb\nc\nd\ne\nf\n", want: []string{"a", "b", "c", "d", "e"}},
		{name: "trims each line", text: "  hello \n\tworld\t", want: []string{"hello", "world"}},
		{name: "strips markdown decoration", text: "- STATUS: not_done\n**I need more context.**\n",
			want: []string{"STATUS: not_done", "I need more context."}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := subagentFallbackOpeningLines(tt.text, subagentFallbackMaxLines, subagentFallbackMaxChars)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d lines %q, want %d %q", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}

	t.Run("caps character count", func(t *testing.T) {
		long := strings.Repeat("x", 1000) + "\nsecond\n"
		got := subagentFallbackOpeningLines(long, subagentFallbackMaxLines, subagentFallbackMaxChars)
		total := strings.Join(got, "\n")
		if len(total) > subagentFallbackMaxChars {
			t.Fatalf("opening head is %d chars, want at most %d", len(total), subagentFallbackMaxChars)
		}
		if strings.Contains(total, "second") {
			t.Error("text beyond the character cap must not be inspected")
		}
	})

	t.Run("caps by runes not bytes", func(t *testing.T) {
		text := strings.Repeat("é", 300) + "\n" + strings.Repeat("é", 200) + "\n"
		got := subagentFallbackOpeningLines(text, subagentFallbackMaxLines, subagentFallbackMaxChars)
		joined := strings.Join(got, "\n")
		if !utf8.ValidString(joined) {
			t.Errorf("opening head is not valid UTF-8: %q", joined)
		}
		if n := utf8.RuneCountInString(joined); n > subagentFallbackMaxChars {
			t.Errorf("opening head is %d runes, want at most %d", n, subagentFallbackMaxChars)
		}
		if n := len(joined); n <= subagentFallbackMaxChars {
			t.Errorf("fixture must exceed the cap in BYTES to be meaningful (got %d)", n)
		}
	})

	t.Run("declaration beyond the window is dropped", func(t *testing.T) {
		text := "one\ntwo\nthree\nfour\nfive\nI could not complete the task.\n"
		got := subagentFallbackOpeningLines(text, subagentFallbackMaxLines, subagentFallbackMaxChars)
		if len(got) != subagentFallbackMaxLines {
			t.Fatalf("got %d lines, want %d", len(got), subagentFallbackMaxLines)
		}
		if status := classifySubagentOpening(got); status != "" {
			t.Errorf("classifySubagentOpening = %q, want no downgrade", status)
		}
	})
}

func TestSubagentInlineResultHeadline(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   string
	}{
		{name: "completed", status: SubagentStatusCompleted, want: "Subagent task completed:"},
		{name: "not_done", status: SubagentStatusNotDone, want: "Subagent task finished but did NOT complete:"},
		{name: "needs_context", status: SubagentStatusNeedsContext, want: "Subagent paused and needs context:"},
		{name: "cancelled", status: SubagentStatusCancelled, want: "Subagent task was cancelled:"},
		{name: "failed", status: SubagentStatusFailed, want: "Subagent task failed:"},
		{name: "empty is neutral", status: "", want: "Subagent task finished:"},
		{name: "unknown is neutral", status: "weird", want: "Subagent task finished:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subagentInlineResultHeadline(tt.status); got != tt.want {
				t.Errorf("subagentInlineResultHeadline(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestBuildSubagentSystemPromptContract(t *testing.T) {
	prompt := buildSubagentSystemPrompt("", "coder", "Coder", "/tmp/ws")
	for _, want := range []string{
		"STATUS: completed | not_done | needs_context",
		"CONTEXT_NEEDED: what is missing (required only for needs_context)",
		"DETAILS:",
		"- The STATUS: line is MANDATORY and must be the FIRST line of your final response.",
		"- If the task is complete, return STATUS: completed. This is the expected outcome for work that is finished.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt is missing %q", want)
		}
	}

	for _, declared := range []string{"completed", "not_done", "needs_context"} {
		if got := normalizeSubagentStatus(declared); got != declared {
			t.Errorf("normalizeSubagentStatus(%q) = %q, contract must round-trip", declared, got)
		}
	}
}
