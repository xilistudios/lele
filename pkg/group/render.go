package group

import (
	"fmt"
	"strings"
)

// RenderTranscript renders the shared transcript as labelled speaker blocks
// in order. Each turn is formatted as:
//
//	[label]: content
//
// When the layer (Turn.Layer) changes relative to the previous turn, a layer
// separator is inserted (e.g. "--- layer 1 ---"), but only if there is more
// than one distinct layer present. For strategies that always use Layer=0
// (round_robin, pipeline), no separators are emitted.
//
// Returns "" if turns is empty.
func RenderTranscript(turns []Turn) string {
	if len(turns) == 0 {
		return ""
	}

	// Check whether multiple layers exist so we only add separators when needed.
	minLayer, maxLayer := turns[0].Layer, turns[0].Layer
	for _, t := range turns[1:] {
		if t.Layer < minLayer {
			minLayer = t.Layer
		}
		if t.Layer > maxLayer {
			maxLayer = t.Layer
		}
	}
	multiLayer := minLayer != maxLayer

	var b strings.Builder
	prevLayer := turns[0].Layer
	for i, t := range turns {
		if multiLayer && (i == 0 || t.Layer != prevLayer) {
			if i > 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "--- layer %d ---\n", t.Layer)
			prevLayer = t.Layer
		}
		if i > 0 && (!multiLayer || t.Layer == prevLayer) {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[%s]: %s", t.Label, t.Content)
	}
	return b.String()
}

// GroupRoleAnnex returns the group-role annex text appended to the agent's
// persona. It describes that the agent is in a collaborative multi-agent panel,
// lists all participants with their roles, states self's role, and includes
// the group task/objective.
func GroupRoleAnnex(self Participant, participants []Participant, task string) string {
	var b strings.Builder

	b.WriteString("You are participating in a collaborative multi-agent panel.\n\n")

	b.WriteString("Participants:\n")
	for _, p := range participants {
		marker := ""
		if p.AgentID == self.AgentID {
			marker = " (you)"
		}
		if p.Role != "" {
			fmt.Fprintf(&b, "- %s (%s)%s\n", p.Label, p.Role, marker)
		} else {
			fmt.Fprintf(&b, "- %s%s\n", p.Label, marker)
		}
	}

	b.WriteString("\n")
	if self.Role != "" {
		fmt.Fprintf(&b, "Your role: %s (%s)\n", self.Label, self.Role)
	} else {
		fmt.Fprintf(&b, "Your role: %s\n", self.Label)
	}

	fmt.Fprintf(&b, "\nObjective: %s", task)
	return b.String()
}

// BuildTurnSystemPrompt builds the full system prompt for a group turn by
// concatenating the agent's persona with the group-role annex. If persona
// is empty, only the annex is returned.
func BuildTurnSystemPrompt(persona string, self Participant, participants []Participant, task string) string {
	annex := GroupRoleAnnex(self, participants, task)
	if persona == "" {
		return annex
	}
	return persona + "\n\n---\n\n" + annex
}
