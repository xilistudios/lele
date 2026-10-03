package agent

// Regression tests for GAP-1: `lele agent -m "..."` printed an empty answer
// because ProcessDirect used the bus-publish contract (runAgentLoop publishes
// the final text to the outbound bus and returns ""). The CLI has no outbound
// subscriber, so the text was published into the void and "" came back.
//
//   - Test A locks the fixed contract: ProcessDirect RETURNs this turn's
//     final text and does not ALSO publish it. The assertion drains the bus
//     absolutely empty, but that is a property of THIS turn shape (fresh
//     session, no /goal set, no /compact, no tool awaiting approval, not a
//     SYSTEM_SPAWN, verbose off) — not a by-construction guarantee: side
//     deliveries like runGoalContinuation, handleCommand's /compact notice,
//     executeWithApproval's prompt and handleSystemSpawn's result can still
//     publish to "cli"/"direct" in other shapes (see ProcessDirect's doc).
//     The resume notice in processMessageWith needs a durable-inbound marker
//     + matching DedupeID that ProcessDirect never sets.
//   - Test B locks the cron contract: ProcessDirectWithChannel still PUBLISHES
//     (cron's delivery IS that publish) and returns "".
//   - Test 3 locks the cron DELIVERY coordinates: channel/chatID come from the
//     JOB while sessionKey is the job's session — they differ, and the session
//     key must never leak into the delivery message.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

// drainOutbound collects everything the bus still holds on its (buffered,
// non-blocking) outbound channel. PublishOutbound happens synchronously inside
// runAgentLoop, so by the time ProcessDirect* returns the decision is already
// final; the short per-receive timeout only bounds the "queue is empty" answer.
func drainOutbound(mb *bus.MessageBus) []bus.OutboundMessage {
	var drained []bus.OutboundMessage
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		msg, ok := mb.SubscribeOutbound(ctx)
		cancel()
		if !ok {
			return drained
		}
		drained = append(drained, msg)
	}
}

// newDirectTestLoop builds the minimal AgentLoop + mock provider recipe proven
// by TestProcessMessage_EphemeralSessionResetsTokenCounts: every test gets its
// own temp config dir, bus and loop, so no test ever drains another's bus.
func newDirectTestLoop(t *testing.T, finalText string) (*AgentLoop, *bus.MessageBus) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   finalText,
			ToolCalls: []providers.ToolCall{},
		},
	}
	return al, msgBus
}

// TestProcessDirectReturnsFinalResponseAndDoesNotPublish locks the GAP-1 fix:
// the CLI contract is to RETURN the turn's final assistant text to its caller
// and to never also publish it. The assertion drains the bus absolutely empty,
// which is stronger than "no message contains the answer" — a caller-side
// regression that re-publishes the returned string (see the old error-branch
// comment in git HEAD: "Returning a non-empty string here lets Run() publish
// the error response") would ship a copy into the drained list.
//
// The empty drain holds for THIS turn shape (fresh session, no /goal set, no
// /compact, no tool awaiting approval, not a SYSTEM_SPAWN, verbose off), not
// by construction: side publishers that ignore sendResponse — runGoalContinuation
// (hardcodes SendResponse:true), commandHandlerImpl.handleCommand's /compact
// notice, toolExecutor.executeWithApproval's non-native approval prompt and
// messageProcessorImpl.handleSystemSpawn's result — can still deliver to
// "cli"/"direct" in other shapes (documented on ProcessDirect). What cannot
// happen here is the final-text publish itself: runAgentLoop's delivery is
// gated by sendResponse, the resume notice in processMessageWith needs a
// durable-inbound marker + matching DedupeID that ProcessDirect never sets,
// and the proactive notices on internal channels are guarded by
// !constants.IsInternalChannel (sessionManagerImpl's compaction notice,
// llmCaller's context-window notice; runAgentLoop's first block is
// RecordLastChannel, also guarded, not a publish). The typing/turn.end
// markers live on the gateway Run path, which ProcessDirect never enters.
// If a future feature deliberately publishes on an internal channel during a
// CLI turn, this test is where that decision surfaces.
func TestProcessDirectReturnsFinalResponseAndDoesNotPublish(t *testing.T) {
	finalText := "DIRECT-FINAL-RESPONSE-7a3f"
	al, msgBus := newDirectTestLoop(t, finalText)

	resp, err := al.providable.ProcessDirect(context.Background(), "hello from the cli", "cli:default")
	if err != nil {
		t.Fatalf("ProcessDirect failed: %v", err)
	}
	if resp != finalText {
		t.Fatalf("ProcessDirect returned %q, want exactly %q", resp, finalText)
	}

	drained := drainOutbound(msgBus)
	if len(drained) != 0 {
		var listed strings.Builder
		for _, out := range drained {
			listed.WriteString("\n  Channel=" + out.Channel + " ChatID=" + out.ChatID + " Content=" + out.Content)
		}
		t.Fatalf("ProcessDirect published %d message(s) to the outbound bus; the CLI contract is to return the text and publish nothing at all:%s",
			len(drained), listed.String())
	}
}

// TestProcessDirectWithChannelStillPublishesAndReturnsEmpty locks the cron
// contract (pkg/tools/cron.go): cron uses ProcessDirectWithChannel with real
// channels and its delivery IS the bus publish, so sendResponse must stay
// true there — publishing the final text on the right channel/chat and
// returning "" to avoid a duplicate.
func TestProcessDirectWithChannelStillPublishesAndReturnsEmpty(t *testing.T) {
	finalText := "DIRECT-FINAL-RESPONSE-7a3f"
	al, msgBus := newDirectTestLoop(t, finalText)

	resp, err := al.providable.ProcessDirectWithChannel(context.Background(), "scheduled job", "telegram:123", "telegram", "123")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	if resp != "" {
		t.Fatalf("ProcessDirectWithChannel returned %q, want exactly \"\" (its delivery is the bus publish)", resp)
	}

	drained := drainOutbound(msgBus)
	for _, out := range drained {
		if out.Content == finalText && out.Channel == "telegram" && out.ChatID == "123" {
			return
		}
	}
	var listed strings.Builder
	for _, out := range drained {
		listed.WriteString("\n  Channel=" + out.Channel + " ChatID=" + out.ChatID + " Content=" + out.Content)
	}
	t.Fatalf("ProcessDirectWithChannel did not publish Content=%q on Channel=telegram ChatID=123; drained outbound:%s",
		finalText, listed.String())
}

// TestProcessDirectWithChannelCronShapePublishesToChannelNotSessionKey locks
// the cron DELIVERY contract the way CronTool.ExecuteJob (pkg/tools/cron.go)
// actually calls it: the delivery channel/chatID come from the JOB
// ("telegram"/"1234567890")
// while sessionKey is the job's session ("cron:daily-report") — deliberately
// DIFFERENT coordinates, which Test B (session == "telegram:123") cannot
// distinguish. The contract has four parts:
//
//  1. return "" exactly (the publish IS the delivery, no caller-side copy);
//  2. exactly ONE bus message carries the final text (a second copy is a
//     duplicate-delivery regression);
//  3. that message's Channel/ChatID are the JOB's coordinates, not the
//     session key;
//  4. the session key never leaks into any drained message's Channel.
func TestProcessDirectWithChannelCronShapePublishesToChannelNotSessionKey(t *testing.T) {
	finalText := "DIRECT-FINAL-RESPONSE-7a3f"
	al, msgBus := newDirectTestLoop(t, finalText)

	resp, err := al.providable.ProcessDirectWithChannel(context.Background(), "daily report", "cron:daily-report", "telegram", "1234567890")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	if resp != "" {
		t.Fatalf("ProcessDirectWithChannel returned %q, want exactly \"\" (the cron delivery IS the bus publish)", resp)
	}

	drained := drainOutbound(msgBus)
	var listed strings.Builder
	for _, out := range drained {
		listed.WriteString("\n  Channel=" + out.Channel + " ChatID=" + out.ChatID + " Content=" + out.Content)
	}

	var withFinalText []bus.OutboundMessage
	for _, out := range drained {
		if out.Content == finalText {
			withFinalText = append(withFinalText, out)
		}
	}
	if len(withFinalText) != 1 {
		t.Fatalf("expected EXACTLY ONE bus message with Content=%q, got %d (a second copy is a duplicate-delivery regression); drained outbound:%s",
			finalText, len(withFinalText), listed.String())
	}
	delivery := withFinalText[0]
	if delivery.Channel != "telegram" || delivery.ChatID != "1234567890" {
		t.Fatalf("cron delivery went to the wrong coordinates: Channel=%q ChatID=%q, want Channel=%q ChatID=%q (the JOB's coordinates); drained outbound:%s",
			delivery.Channel, delivery.ChatID, "telegram", "1234567890", listed.String())
	}
	for _, out := range drained {
		if out.Channel == "cron:daily-report" {
			t.Fatalf("the session key leaked into the delivery coordinates: Channel=%q (sessionKey must never be a delivery channel); drained outbound:%s",
				out.Channel, listed.String())
		}
	}
}

// TestProcessDirectWithActiveGoalPinsSideDeliveryLimitation is executable
// documentation for the GAP-1 scope note on ProcessDirect: sendResponse=false
// governs only THIS turn's final text. An active goal still runs its
// continuation machinery after ProcessDirect's turn, and that machinery
// ignores the flag — runGoalContinuation publishes (here: the budget-exhausted
// notice, which with maxTurns=1 and no judge is the loop's only output) to
// "cli"/"direct", where a plain `lele agent` run never subscribes. The
// assertions pin all three facts: the caller still gets exactly the first
// turn's text, the notice still lands on the bus, and it is NOT folded into
// the return value. Fixing the CLI blind spot (goal/compact/approval/spawn
// side deliveries) is a deliberate follow-up; when it happens, update this
// test as part of that decision.
func TestProcessDirectWithActiveGoalPinsSideDeliveryLimitation(t *testing.T) {
	finalText := "DIRECT-FINAL-RESPONSE-4e8a"
	al, msgBus := newDirectTestLoop(t, finalText)

	const sessionKey = "cli:goal-tripwire"
	al.goalManager.Set(sessionKey, "keep the CLI contract honest", 1)

	resp, err := al.providable.ProcessDirect(context.Background(), "start working", sessionKey)
	if err != nil {
		t.Fatalf("ProcessDirect failed: %v", err)
	}
	if resp != finalText {
		t.Fatalf("ProcessDirect returned %q, want exactly %q — side deliveries must not leak into the CLI return value", resp, finalText)
	}

	var notice *bus.OutboundMessage
	drained := drainOutbound(msgBus)
	for i, out := range drained {
		if strings.Contains(out.Content, "Goal budget exhausted") {
			notice = &drained[i]
		}
	}
	if notice == nil {
		var listed strings.Builder
		for _, out := range drained {
			listed.WriteString("\n  Channel=" + out.Channel + " ChatID=" + out.ChatID + " Content=" + out.Content)
		}
		t.Fatalf("expected the goal budget-exhausted notice on the outbound bus (documented CLI blind spot); drained:%s", listed.String())
	}
	if notice.Channel != "cli" || notice.ChatID != "direct" {
		t.Errorf("side delivery went to Channel=%q ChatID=%q, want cli/direct (ProcessDirect's synthetic coordinates)", notice.Channel, notice.ChatID)
	}
}
