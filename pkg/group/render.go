package group

import (
	"fmt"
	"strings"
)

func RenderTranscript(turns []Turn) string {
	if len(turns) == 0 {
		return ""
	}

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

func BuildTurnSystemPrompt(persona string, self Participant, participants []Participant, task string) string {
	annex := GroupRoleAnnex(self, participants, task)
	if persona == "" {
		return annex
	}
	return persona + "\n\n---\n\n" + annex
}
