package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Tests for parseSubagentOutcome. The regression these lock down: a subagent
// that finished its work and declared STATUS: completed used to be reported as
// not_done or needs_context, because a whole-document substring scan read the
// problems the report described ("missing", "failed to", "could not") as proof
// that the run itself had failed. A declared status is now final, and the
// fallback only classifies output that never declared one, by its opening line.
//
// The synthetic guard messages come from pkg/tools/toolloop.go directly
// (toolLoopMaxIterationsContent / toolLoopEmptyResponseContent) so a change
// there cannot drift away from what these tests assert.

// longSuccessReport is a realistic engineering report: it declares completed
// and then narrates every error it hit on the way, which is exactly the shape
// the old heuristic mistook for an unfinished task.
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
			// The exact regression: a completed run whose details describe
			// problems it already fixed.
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
			// No contract delivered, but the opening states success, so the
			// words further down must not downgrade it.
			name: "prose success mentioning past failures",
			raw: "Refactored the CSV importer and the suite is green.\n\n" +
				"The test failed to compile before the fix because a helper was missing,\n" +
				"and I could not reproduce initially until I set TMPDIR. Both are resolved.\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Refactored the CSV importer and the suite is green.",
		},
		{
			// DETAILS: is a terminal section, so a quoted contract line inside
			// the body cannot rewrite the outcome.
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
			// Locks the window: a declaration past the opening lines is report
			// body and does not classify the outcome.
			name: "declaration beyond the opening window is ignored",
			raw: "Renamed the module and updated the callers.\n" +
				"Tests pass and the package builds.\n" +
				"The changelog entry is written.\n" +
				"The README section is updated.\n" +
				"Coverage numbers are recorded below.\n" +
				"I could not complete the deployment step, that one is on you.\n",
			wantStatus: SubagentStatusCompleted,
		},

		// --- False-positive guards (B1) ------------------------------------
		// Report prose ABOUT the code that was just fixed. The bare subjects
		// only classify when they carry a delimiter or a terminator, so none of
		// these may downgrade a run that never declared a status.
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
			name: "headline plus prose about blocked jobs stays completed",
			raw:  "# Queue fix\n\nBlocked jobs are now retried three times.\n",
			// The heading bullet is stripped as decoration, so the summary is
			// the raw first line of the body.
			wantStatus:         SubagentStatusCompleted,
			wantContextRequest: "",
		},
		{
			name:        "prose about needs-context inference stays completed",
			raw:         "Needs context inference is now part of the planner.\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Needs context inference is now part of the planner.",
		},

		// --- Openings that were unreachable before (M1/M4) -----------------
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
			name: "inability admitted mid sentence",
			raw:  "I've done most of it but I could not complete the task.\n",
			// Deliberately the one un-anchored pattern: first person +
			// inability + the task itself.
			wantStatus: SubagentStatusNotDone,
		},
		{
			name:       "decorated inability",
			raw:        "**I was unable to complete the task.**\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// EDIT 3: the context request is backfilled from the summary, and
			// the parent must read it as prose -- the markdown bullet is
			// stripped, only the explicit CONTEXT_NEEDED: field keeps its
			// value verbatim.
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

		// --- Decoration-tolerant field parsing (M4) ------------------------
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
			// A trailing quoted status must not downgrade a declared outcome:
			// the header declaration is authoritative and the closing rule
			// only speaks when the header said nothing. This was the original
			// bug -- a report whose last line quotes the contract flipping a
			// genuinely completed run to not_done.
			name:        "closing status never contradicts a header declaration",
			raw:         "STATUS: completed\nSUMMARY: ok\nDETAILS:\nbody\n\n**STATUS: not_done**",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "ok",
		},
		{
			// The case the closing rule exists for: the report declares no
			// status in its header and closes with one, so the last line is
			// the verdict and must be rescued.
			name:        "closing status rescues a header without a declaration",
			raw:         "SUMMARY: half done\nDETAILS:\nbody\n\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "half done",
		},
		{
			// A STATUS: line inside a fenced code block is a quote, not a
			// declaration, even when the fence closes the report.
			name:        "fenced status line is a quote not a closing declaration",
			raw:         "SUMMARY: wrote the docs\nDETAILS:\nThe contract is:\n```\nSTATUS: not_done\n```\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "wrote the docs",
		},
		{
			// ...while the same line quoted inside the body stays inert.
			name:        "status quoted inside details does not count",
			raw:         "STATUS: completed\nSUMMARY: ok\nDETAILS:\nline\nSTATUS: not_done\nmore body text here\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "ok",
		},
		{
			// The near-miss vocabulary: a declared in-progress value is a
			// declaration that the run is NOT finished. Leaving these out of
			// the vocabulary resolved them to nothing and reported the run
			// completed -- the direction this parser must never get wrong --
			// while the equally unfinished "incomplete" was not_done.
			name:        "in progress declaration is not a success",
			raw:         "STATUS: in progress\nSUMMARY: half done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "half done",
		},
		{name: "partial declaration is not a success", raw: "STATUS: partial\n", wantStatus: SubagentStatusNotDone},
		{
			// "pending" is ambiguous between "waiting on something" and
			// "pending review by the user", so it resolves to the TERMINAL
			// side: a needs_context run never becomes terminal, which would
			// leave the tasks depending on it unsatisfied forever. The nuance
			// still reaches the parent through the summary.
			name:       "pending declaration is terminal, not a success",
			raw:        "STATUS: pending\n",
			wantStatus: SubagentStatusNotDone,
		},
		{name: "wip declaration is not a success", raw: "STATUS: wip\n", wantStatus: SubagentStatusNotDone},
		{
			// FACT 1 regression: a declaration that merely CONTAINS a
			// separator is still a declaration. The old alternation guard
			// rejected any value with '/' in it, so the single most natural
			// way a model says "not finished" fell through to the completed
			// default.
			name:       "in progress with a file-count clause is not a success",
			raw:        "STATUS: in progress (3/8 files done)\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// ...and the same for a path inside the clause: the '/' in
			// pkg/tools/registry.go is not an alternation of statuses.
			name:       "not_done with a file path clause is not a success",
			raw:        "STATUS: not_done - pkg/tools/registry.go unreadable\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// CROSS-CLASS REGRESSION: the guard used to fire on ANY
			// disagreement between the alternation parts, so an enumeration
			// that contains no success at all was read as a printed legend,
			// its declaration was discarded, and the run was reported
			// completed. A legend must include a success; without one this is
			// just a verbose way of saying "not finished".
			name:       "non-success enumeration is a declaration, not a legend",
			raw:        "STATUS: blocked/pending - the CI token is missing\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// ...the same with the "or" spelling: needs_context is not a
			// success either, so nothing here enumerates the vocabulary.
			name:       "non-success or-enumeration is a declaration",
			raw:        "STATUS: failed or needs_context\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// A value that is not a status at all is not authoritative; the
			// opening window cannot see it either (it is a field line, not a
			// declaration), so the result is the completed default rather
			// than a downgrade.
			name:        "unrecognised status value falls through to the default",
			raw:         "STATUS: see the details below\nSUMMARY: half done\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "half done",
		},
		{
			// A declared value outside the vocabulary is still an attempt at
			// a declaration, so it is read with the same opening-line
			// classifier the contract-less fallback uses: "could not
			// complete" is not a token, it is a sentence, and it says the
			// run is unfinished. Removing the classifySubagentOpening call
			// in the STATUS arm defaults this run to completed and this
			// subtest dies.
			name:       "out-of-vocabulary status value is classified",
			raw:        "STATUS: could not complete the task, CI is red\nSUMMARY: half done\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// Companion of the fixture above for the needs_context class.
			// The summary defaults to the declaration line, so the parent's
			// "Context needed:" is the request itself: the STATUS: prefix is
			// stripped on the way (a parent that renders "Context needed:
			// STATUS: I need more context ..." is quoting the parser).
			name:               "out-of-vocabulary context request is classified",
			raw:                "STATUS: I need more context about the deploy target\n",
			wantStatus:         SubagentStatusNeedsContext,
			wantContextRequest: "I need more context about the deploy target",
			wantSummary:        "I need more context about the deploy target",
		},
		{
			// The classifier is fed the value with the same noise pass the
			// token path applies, so decoration tolerance does not stop at
			// the token boundary: an emoji or a quoted phrase in front of the
			// sentence must not hide it. Dropping the noise pass from the
			// STATUS arm leaves this run at the completed default.
			name:       "out-of-vocabulary value is classified through decoration",
			raw:        "STATUS: ❌ \"could not complete\" the migration\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// "Almost done" is the near-miss value a model declares outside the
			// vocabulary. Without the qualified-completion opener the run is
			// reported completed while one test is still red.
			name:       "qualified completion is a not_done declaration",
			raw:        "STATUS: almost done but one test is red\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// An echo cannot upgrade: a model that restates the contract (or
			// quotes the vocabulary) after its own header cannot rewrite the
			// verdict the report opened with. With plain last-wins parsing the
			// echoed "STATUS: completed" turns a not_done run into a success
			// -- the dangerous direction -- and this subtest dies.
			name:        "an echo cannot upgrade the outcome",
			raw:         "STATUS: not_done\nSUMMARY: parser half fixed\nSTATUS: completed\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "parser half fixed",
		},
		{
			// The marker test must accept any info string, not just a short
			// language tag: "```c#" (or "```console title=session") is a real
			// opener, and failing to recognise it leaves its own closing
			// marker to be read as an opener, which inverses the fence state
			// for the rest of the report and hides the genuine closing
			// STATUS: below. Narrowing the regexp back to a language-tag-only
			// pattern kills this subtest.
			name:        "fence with a punctuated info string still delimits",
			raw:         "SUMMARY: x\nDETAILS:\n```c#\nvar x = 1;\n```\n\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "x",
		},
		{
			// Tilde fences are fences too. An unclosed one must hide the
			// STATUS: line it holds exactly like a backtick fence does;
			// removing "~" from the marker patterns turns that quoted line
			// into a closing declaration and flips this run to not_done.
			name:        "tilde fence quotes its status",
			raw:         "SUMMARY: x\nDETAILS:\n~~~\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "x",
		},
		{
			// An INFERRED status is not a declaration: it only fills an
			// outcome no declaration decided. A declared token later in the
			// same header therefore still wins. Letting the inference set the
			// authoritative flag reports this run as needs_context and kills
			// this subtest.
			name:       "an inferred status does not outrank a declaration",
			raw:        "STATUS: I need more context about the deploy target\nSUMMARY: s\nSTATUS: completed\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			// Same rule against the closing rescue: the report closes with an
			// explicit STATUS:, so the closing rule must still be in play
			// after a STATUS: value that was only inferred. Giving the
			// inference declaration authority reports needs_context here.
			name:       "an inferred status leaves the closing rule in play",
			raw:        "STATUS: I need more context about the deploy target\nSUMMARY: s\nDETAILS:\nbody\n\nSTATUS: completed\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			// stripOuterCodeBlock and the line parser must agree on what an
			// opening fence is: if the wrapper strip rejects a fence whose
			// info string has a space (or that uses tildes) while the line
			// parser toggles on it, the whole report -- declaration included
			// -- is read as quoted body text and an unfinished run is
			// reported completed. Narrowing the strip back to a lang-only
			// backtick fence kills these two subtests.
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
			// A fence can also be opened behind blockquote/bullet decoration,
			// and its bare closer must then close it. Without decoration
			// support the opener is ignored while the closer opens a block,
			// which swallows the declaration and reports completed.
			name:       "decorated fence opener is a fence",
			raw:        "- ```go\ncode\n```\n\nSTATUS: not_done\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// Inside a fenced block only a BARE run closes it: "+ ```go" is a
			// quoted diff line, not a closer, and letting it toggle closes the
			// diff early and re-opens a block on the real closer, which hides
			// the closing STATUS: below. Dropping the inFence branch of
			// subagentFenceState.toggle reports this run completed.
			name:        "a decorated fence line inside a block is content",
			raw:         "SUMMARY: docs\nDETAILS:\nThe patch adds a fence opener:\n```diff\n+ ```go\n```\n\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "docs",
		},
		{
			// A decorated fence must be closable by a decorated marker: the
			// opener accepts "> ```" / "- ```", so the state machine has to
			// accept the same form as a closer -- otherwise the block never
			// closes, every header field is swallowed and the run is reported
			// completed.
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
			// An unclosed wrapper (the model wrapped its answer and the stream
			// was cut, or it forgot the marker) still wraps a report: the
			// content is unwrapped when no other fence is involved. Without
			// that branch the whole report is quoted body text and a declared
			// not_done is reported completed.
			name:       "unclosed wrapper is unwrapped",
			raw:        "```text\nSTATUS: not_done\nSUMMARY: x\nDETAILS:\nbody\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// ...but a report that merely STARTS with a complete code block is
			// not wrapped: stripping its markers would invert the fence parity
			// of everything after it and hide the declaration.
			name:        "a leading code block is not a wrapper",
			raw:         "```\ncode\n```\n\nSUMMARY: x\nDETAILS:\nbody\n\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "x",
		},
		{
			// A report that starts with a closed block and ends with a bare
			// fence run is not a wrapper: its body still holds a fence, and
			// stripping the outer pair would invert the parity of everything
			// in between, pushing the declaration inside a block the report
			// never opened. Without the inner-fence guard this run is reported
			// completed.
			name:       "a closed block at the start and a stray run at the end",
			raw:        "```\na\n```\n\nSTATUS: not_done\n\n```\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// A tilde divider cannot close a backtick block (markdown requires
			// the same character): letting it close inverts the state and the
			// genuine closing STATUS: ends up inside a block.
			name:       "a tilde line cannot close a backtick block",
			raw:        "SUMMARY: x\nDETAILS:\n```\n~~~\n```\n\nSTATUS: not_done\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// A sign-off line after the verdict carries no content: counting it
			// as the report's last line defeats the closing rescue.
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
			// A pasted example payload (a JSON document inside the body) is not
			// the report's declaration: the text STATUS: line wins even though
			// the JSON is found first by the extractor.
			name:       "a pasted json example cannot override the declaration",
			raw:        "STATUS: not_done\nSUMMARY: x\nDETAILS:\nThe payload is:\n```json\n{\"status\":\"completed\",\"summary\":\"s\"}\n```\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// A trailing fence line is decoration, not content: counting it as
			// the report's last line pushes the closing veto past a genuine
			// closing STATUS: and reports this run completed.
			name:        "trailing tilde divider does not defeat the closing status",
			raw:         "SUMMARY: half done\nDETAILS:\nbody\n\nSTATUS: not_done\n\n~~~\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "half done",
		},
		{
			// The mirror of the inferred-status fixtures: a declaration that
			// comes FIRST is not overridden by a later inferred one. Removing
			// the `if explicit` guard in the inferred branch reports these runs
			// as needs_context.
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
			// ...but a later DOWNGRADE is honoured: a model that revises its
			// verdict in the header ("the last test still fails") is the last
			// word on the run, and reporting it completed would over-report
			// work that is not finished. A plain first-wins guard kills this
			// subtest.
			name:        "a late downgrade is honoured",
			raw:         "STATUS: completed\nSUMMARY: wrote the migration\nSTATUS: not_done, the last test still fails\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "wrote the migration",
		},

		// --- Verification-review fixtures -------------------------------
		// Each one pins a hole (or a previously unpinned behaviour) found
		// in the parser; the comments name the hazard and the mutant each
		// fixture is meant to die under.
		{
			// T-1: an UNCLOSED fence in the details body hides its STATUS:
			// line from the closing-declaration guard even though the
			// decoration-skipping lastNonEmptyIdx makes that line the last
			// one of the report. Removing the `if !fence.open` guard around
			// the recorded declaration (flipping it to `if true`) lets the
			// fenced quote hijack the outcome and this subtest dies.
			name:        "T1 unclosed fence keeps the closing rule out",
			raw:         "SUMMARY: wrote the docs\nDETAILS:\nThe shape is:\n```\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "wrote the docs",
		},
		{
			// T-1b: one line wrapped in inline triple backticks IS the
			// fence marker line, so it is consumed before the field switch
			// and before the closing-declaration guard: its STATUS: text
			// is never read. Decided as written: still completed. This
			// fixture (with T-1 and the fenced-quote guards) pins fence
			// tracking as a whole -- removing the ``` marker handling lets
			// the decoration-stripped line become a bare closing
			// declaration and flips this run to not_done.
			name:        "T1 inline fenced status line is body text",
			raw:         "SUMMARY: x\nDETAILS:\n```STATUS: not_done```\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "x",
		},
		{
			// Only a BARE fence marker toggles the fenced-block state. A
			// details line that merely OPENS with inline code ("```go vet
			// ./...```") is not a block marker: toggling on it leaves the
			// block open and hides the genuine closing STATUS: line below,
			// so this run would be reported completed. Flipping the bare-
			// marker test back to a plain HasPrefix mutant kills this
			// subtest.
			name:        "inline code fence does not swallow the closing status",
			raw:         "SUMMARY: x\nDETAILS:\n```go vet ./...```\n\nSTATUS: not_done\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "x",
		},
		{
			// T-2: strict vocabulary. "see the details below" is outside it,
			// so the header declaration is NOT authoritative and the genuine
			// closing STATUS: line at the tail of a delivered contract is
			// still rescued. A fromToken that defaults unknown values to
			// completed (the old normalize behaviour) declares on the
			// first line and this subtest dies.
			name:        "T2 unrecognised header value leaves the closing rule in play",
			raw:         "STATUS: see the details below\nSUMMARY: half done\nDETAILS:\nbody\n\nSTATUS: not_done",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "half done",
		},
		{
			// M-1: prose that merely ends with a Status: line. The first
			// line is not a contract field, so the header is over: the
			// "Status:" line declares nothing, and with no DETAILS: parsed
			// the closing rule has no recorded declaration to act on.
			// Deleting the switch's default arm (headerEnded) re-arms the
			// hijack and this subtest dies.
			name:       "M1 prose ending in a status line is inert",
			raw:        "The states are: queued, then\nStatus: blocked\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			// M-1 second direction: past-tense narration about blocked
			// work plus a Status: tail line.
			name:       "M1 prose about blocked work ending in a status line is inert",
			raw:        "The task was blocked, retry\nStatus: blocked\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			// The flip side of the header gate: a model that writes a
			// preamble before the contract must not lose its declaration.
			// The contract's own uppercase spelling is recognised anywhere,
			// so the header gate cannot turn a genuine not_done into
			// completed. Dropping subagentIsContractField from the gate
			// kills this subtest.
			name: "preamble before the contract keeps the declaration",
			raw: "Here is my report:\n" +
				"STATUS: not_done\n" +
				"SUMMARY: no write access\n" +
				"DETAILS:\nCould not write the files.\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "no write access",
		},
		{
			// ...while the ordinary-case spelling after prose stays prose,
			// even though it is field-shaped.
			name:       "lower-case status after prose stays prose",
			raw:        "Here is my report:\nstatus: not_done\nSummary: done\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			// M-2: the vocabulary resolves on the LEADING token; a trailing
			// clause is prose bolted onto a real declaration. Without the
			// prefix resolution "not_done (blocked by CI)" is unrecognised
			// and silently defaults to completed.
			name:       "M2 not_done with a trailing clause",
			raw:        "STATUS: not_done (blocked by CI)\n",
			wantStatus: SubagentStatusNotDone,
		},
		{name: "M2 blocked with a by clause", raw: "STATUS: blocked by the CI outage\n", wantStatus: SubagentStatusNotDone},
		{name: "M2 failed with an exit code", raw: "STATUS: failed (exit 1)\n", wantStatus: SubagentStatusNotDone},
		{name: "M2 not complete two-word value", raw: "STATUS: not complete\n", wantStatus: SubagentStatusNotDone},
		{name: "M2 completed with a trailing clause", raw: "STATUS: completed and all tests green\n", wantStatus: SubagentStatusCompleted},
		{
			// M-3 both directions. A hyphen joins words inside compounds,
			// it is not a clause break: with '-' still in the delimiter
			// class this prose is misread as a failure declaration.
			name:       "M3 and-hyphen compound prose is inert",
			raw:        "Blocked-and-later-retried queue jobs now finish cleanly.\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			// ...and the by-hyphen compound, unpinned behaviour one: the
			// old delimiter set matched "Blocked-by-..." as a declaration.
			name:       "M3 by-hyphen compound prose is inert",
			raw:        "Blocked-by-design queue jobs now finish cleanly.\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			// ...while the spaced preposition IS the natural failure
			// opener, unpinned behaviour two: before the fix "Blocked on
			// CI outage." classified as nothing and defaulted to
			// completed, hiding a genuine unfinished run.
			name:       "M3 blocked on a preposition declares",
			raw:        "Blocked on CI outage.\n",
			wantStatus: SubagentStatusNotDone,
		},
		{name: "M3 stuck on a preposition declares", raw: "Stuck on circular import.\n", wantStatus: SubagentStatusNotDone},
		{
			// M-4: models print the contract template inside a fence
			// BEFORE the real header. Nothing inside the prefatory fence
			// may be parsed -- no status, no summary (wantSummary proves
			// the summary was not taken from inside the fence: it is the
			// report's own opening line, the details default). Two guards
			// cover this deliberately: the fence skip AND the header gate
			// (the opening prose line already ends the header). Deleting
			// either one alone keeps the run completed; deleting both (the
			// pre-fix shape) parses the template and flips it to
			// not_done.
			name:        "M4 fenced contract template is a preamble",
			raw:         "Here is the shape I will report:\n```text\nSTATUS: not_done\n```\nI fixed the parser and the suite is green.\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "Here is the shape I will report:",
		},
		{
			// M-4 companion: the fence is a quote, not a header -- the
			// real header AFTER the template still declares. (Without
			// stripping the outer fence this text reaches the line loop,
			// where the fence consumed as a marker line keeps the header
			// open for the fields that follow.)
			name:        "M4 header after a quoted contract still parses",
			raw:         "```text\nSTATUS: not_done\n```\n\nSTATUS: completed\nSUMMARY: ok\n",
			wantStatus:  SubagentStatusCompleted,
			wantSummary: "ok",
		},
		{
			// m-1: a trailing pure-decoration line ("---") is skipped when
			// finding the last non-empty line, so it cannot push the tail
			// past a genuine closing STATUS: line. Counting decoration
			// lines in lastNonEmptyIdx makes the closing rule miss idx by
			// one and this subtest dies.
			name:        "m1 trailing divider does not defeat the closing status",
			raw:         "SUMMARY: half done\nDETAILS:\nbody\n\nSTATUS: not_done\n\n---\n",
			wantStatus:  SubagentStatusNotDone,
			wantSummary: "half done",
		},
		{
			// Window: a '#'/Scope preamble used to eat the whole
			// three-line window; the declaration on the fifth non-empty
			// line is now inside it and still classifies.
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

// TestParseSubagentOutcomeCoverageGaps pins parser rules that no table fixture
// reached -- each one a mutant that used to survive the whole package.
func TestParseSubagentOutcomeCoverageGaps(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantStatus  string
		wantSummary string
	}{
		{
			// A status quoted MID body is not a closing declaration: the
			// closing rule only honours a status that ends the report.
			name:       "status quoted mid body is inert",
			raw:        "SUMMARY: x\nDETAILS:\nbody\nSTATUS: not_done\nmore body\n",
			wantStatus: SubagentStatusCompleted,
		},
		{
			// A legend of the vocabulary is not a declaration (the system
			// prompt itself prints one), so the fallback still classifies the
			// body.
			name:       "vocabulary legend is not a declaration",
			raw:        "STATUS: completed | not_done | needs_context\nDETAILS:\nI could not complete the task.\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// The parenthesis is not a delimiter the opening-line
			// classifier accepts, so the only thing that rescues the verdict
			// is the doubled space the noise pass leaves behind ("in
			// progress  the CLI flag is unimplemented" splits into the
			// vocabulary head "in progress"). Drop the doubled-space rule --
			// or the parenthesis here -- and this subtest dies: the same
			// value without "(the ...)" classifies as completed.
			name:       "status value keeps its clause after the noise pass",
			raw:        "STATUS: almost finished (the CLI flag is unimplemented)\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// Ordered-list decoration is stripped before the field is read.
			name:       "ordered list decoration",
			raw:        "1. STATUS: not_done\n2. SUMMARY: blocked on CI\n",
			wantStatus: SubagentStatusNotDone,
		},
		{
			// PROGRESS: may interleave the header and must not end it.
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

	// The fallback summary is decoration-stripped: a report that never wrote a
	// SUMMARY: line and opens with a bullet must not hand the parent a headline
	// that starts with "- " or a fence marker.
	if got := parseSubagentOutcome("- implemented the rate limiter and tests\n").Summary; got != "implemented the rate limiter and tests" {
		t.Errorf("fallback Summary = %q, want the bullet stripped", got)
	}

	// A JSON payload that carries details still gets its summary cleaned, and
	// a multi-tick wrapper leaves no stray marker in the details.
	jsonOutcome := parseSubagentOutcome(`{"status":"needs_context","details":"STATUS: I need more context about X"}`)
	if jsonOutcome.Summary != "I need more context about X" {
		t.Errorf("json Summary = %q, want %q", jsonOutcome.Summary, "I need more context about X")
	}
	if got := parseSubagentOutcome("````\nSTATUS: completed\nSUMMARY: s\nDETAILS:\nbody\n````").Details; got != "body" {
		t.Errorf("four-tick wrapper Details = %q, want %q", got, "body")
	}
}

// TestParseSubagentOutcomeExplicitStatusIsAuthoritative asserts the union
// invariant: whenever the subagent declared a status -- on a STATUS: line or in
// a JSON payload -- the returned status equals the declaration, no matter what
// the body says. The expected status is hardcoded per token: deriving it from
// normalizeSubagentStatus would let a wrong vocabulary mapping pass.
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

			// The JSON branch returns before the body is ever classified, so
			// the details text is inert here: one payload is enough to pin that
			// a declared JSON status is authoritative.
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
		// Values as models actually render them after a bolded field prefix.
		{name: "bold not_done", value: "**not_done**", want: SubagentStatusNotDone},
		{name: "sentence with space and period", value: "not done.", want: SubagentStatusNotDone},
		{name: "inline code completed", value: "`completed`", want: SubagentStatusCompleted},
		// Emoji and other decoration around the token are not part of the
		// vocabulary, and ignoring them used to report a declared not_done as
		// completed -- the dangerous direction.
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

// TestSubagentStatusFromToken pins the strict half of the vocabulary split:
// only a value the parser recognises claims authority over the outcome.
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
		// "pending" is ambiguous ("waiting on CI" vs "pending review by the
		// user"), so it resolves to the TERMINAL side: a needs_context
		// dependency never reaches a terminal state and would stall every
		// task waiting on it, while the nuance travels to the parent in the
		// summary. waiting/paused are unambiguous asks for input and stay
		// needs_context.
		{name: "pending is terminal", value: "pending", want: SubagentStatusNotDone, wantOK: true},
		{name: "wip is unfinished", value: "wip", want: SubagentStatusNotDone, wantOK: true},
		{
			// A declaration that merely CONTAINS a separator is not a legend:
			// the parts split on it resolve to at most one status, so the
			// leading token keeps its authority. Rejecting these made the most
			// natural "I am not finished" (a file count in parentheses) read as
			// completed.
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
			// Same-status alternation is a restatement, not a legend.
			name:   "synonym alternation is a declaration",
			value:  "blocked/failed",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			// CROSS-CLASS REGRESSION: an enumeration of alternatives that
			// contains NO success is not a legend, it is one unambiguous
			// non-success declaration. Rejecting it as a legend stripped the
			// authority off the declaration and the parser then reported the run
			// completed -- the exact direction this guard exists to prevent.
			// The leading token of the whole value carries the verdict.
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
			// ...and the same rule read the other way round: the enumeration is
			// of asks-for-input, so it declares needs_context.
			name:   "needs_context enumeration stays needs_context",
			value:  "waiting/blocked",
			want:   SubagentStatusNeedsContext,
			wantOK: true,
		},
		{
			// The two halves of the vocabulary rule meet here: because
			// "pending" is not_done (ITEM 2), the enumeration agrees with
			// itself and never raises the legend question at all -- which is
			// also why this row only dies when ITEM 1 and ITEM 2 are reverted
			// together (reverting ITEM 1 alone leaves nothing to disagree
			// about).
			name:   "in progress with a pending tail",
			value:  "in progress/pending",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{
			// Same-status restatement spelled with "or": "partially done" is
			// not_done too, so there is no disagreement to mistake for a menu.
			name:   "not_done with a partially-done tail",
			value:  "not_done or partially done",
			want:   SubagentStatusNotDone,
			wantOK: true,
		},
		{name: "unrecognised prose", value: "see the details below", want: "", wantOK: false},
		{name: "empty", value: "", want: "", wantOK: false},
		// A legend of the vocabulary is not a declaration (the system prompt
		// itself prints one): a value whose alternatives resolve to two or
		// more DIFFERENT statuses AND include a success is offering a menu, so
		// it declares nothing. Order-independent: "not_done | completed" is
		// the same legend as "completed | not_done".
		{name: "three-way legend", value: "completed | not_done | needs_context", want: "", wantOK: false},
		{name: "two-way legend", value: "completed/not_done", want: "", wantOK: false},
		{name: "two-way or legend", value: "completed or not_done", want: "", wantOK: false},
		{name: "reversed legend", value: "not_done | completed", want: "", wantOK: false},
		{name: "upper-case legend", value: "COMPLETED | NOT_DONE", want: "", wantOK: false},
		{name: "decorated legend", value: "**completed | not_done**", want: "", wantOK: false},
		// A trailing clause must not hide the declaration: the vocabulary
		// resolves on the leading token, shortest prefix first -- no token is a
		// word-prefix of another token with a different status, so the order
		// only decides how quickly an unknown head gives up.
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

// TestClassifySubagentOpening covers the fallback classifier directly, so the
// anchored patterns and the opening window stay documented even if the parser
// around them changes.
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
		// Successful prose mentions: the patterns need a subject and a
		// delimiter, so narration about the code never classifies.
		{name: "plain success", text: "Implemented the endpoint and added tests.", want: ""},
		{name: "past tense mentions", text: "The test failed to compile before the fix.", want: ""},
		{name: "unrelated could not", text: "I could not find a better name for it.", want: ""},
		{name: "word missing in body", text: "Renamed the field so the missing case is covered.", want: ""},
		{name: "blocked jobs prose", text: "Blocked jobs are now retried three times before failing.", want: ""},
		{name: "stuck uploads prose", text: "Stuck uploads are now surfaced in the UI and retried.", want: ""},
		{name: "task incomplete prose", text: "Task incomplete handling was added to the API and tested.", want: ""},
		{name: "not done items prose", text: "Not done items are now visible in the report.", want: ""},
		// Delimiter directions for the bare subjects: spaced prepositions
		// open real failure declarations, hyphens only join words inside
		// prose compounds.
		{name: "blocked on preposition", text: "Blocked on CI outage.", want: SubagentStatusNotDone},
		{name: "stuck on preposition", text: "Stuck on circular import.", want: SubagentStatusNotDone},
		{name: "blocked with clause", text: "Blocked by a locked index file.", want: SubagentStatusNotDone},
		{name: "and-hyphen compound prose", text: "Blocked-and-later-retried queue jobs finish cleanly.", want: ""},
		{name: "by-hyphen compound prose", text: "Blocked-by-design queue jobs finish cleanly.", want: ""},
		{name: "needs context inference prose", text: "Needs context inference is now part of the planner.", want: ""},
		// Qualified completion: a verdict when it stands as a clause, prose
		// when the completion word is attributive (the B-class rule every bare
		// subject in this table follows). Dropping the delimiter requirement
		// reports each of the three prose lines below as not_done, which is the
		// original bug on a finished run.
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

// TestClassifySubagentOpeningEarliestLineWins pins the line-major order: the
// first line that declares anything decides, it is not a class-level vote.
func TestClassifySubagentOpeningEarliestLineWins(t *testing.T) {
	// Line 1 declares not_done, line 2 asks for context: line 1 wins.
	text := "I could not complete the task.\nI need more information about the target host.\n"
	lines := subagentFallbackOpeningLines(text, subagentFallbackMaxLines, subagentFallbackMaxChars)
	if got := classifySubagentOpening(lines); got != SubagentStatusNotDone {
		t.Errorf("classifySubagentOpening = %q, want %q (the first line declares)", got, SubagentStatusNotDone)
	}

	// Reversed order: the context request is the first declaration.
	text = "I need more information about the target host.\nI could not complete the task.\n"
	lines = subagentFallbackOpeningLines(text, subagentFallbackMaxLines, subagentFallbackMaxChars)
	if got := classifySubagentOpening(lines); got != SubagentStatusNeedsContext {
		t.Errorf("classifySubagentOpening = %q, want %q", got, SubagentStatusNeedsContext)
	}

	// Within ONE line both classes match; needs_context keeps its precedence.
	text = "I need more context; I cannot complete the task.\n"
	lines = subagentFallbackOpeningLines(text, subagentFallbackMaxLines, subagentFallbackMaxChars)
	if got := classifySubagentOpening(lines); got != SubagentStatusNeedsContext {
		t.Errorf("classifySubagentOpening = %q, want %q (needs_context first within a line)", got, SubagentStatusNeedsContext)
	}
}

// TestSubagentFallbackOpeningLinesWindow pins the head-of-output window: the
// first non-empty trimmed lines only, capped by character count.
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
		// Two multi-byte lines that together exceed the cap: the truncation
		// must land on a rune boundary, so a regression to byte slicing (which
		// would cut an é in half) is caught here.
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

// TestSubagentInlineResultHeadline pins the inline `subagent` tool header: the
// headline must agree with the declared status, because the parent LLM reads it
// first and an unconditional "completed" would report a failed run as success.
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

// TestBuildSubagentSystemPromptContract keeps the prompt and the parser in sync:
// the statuses the contract advertises must normalize to the exported constants.
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
