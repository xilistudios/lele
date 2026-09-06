// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

// Package harness implements user-defined slash commands: markdown files with
// frontmatter (description/agent/model/allow_shell) plus a prompt template, or
// inline definitions coming from config.json.
//
// It is a leaf package on purpose: it must not import pkg/agent or
// pkg/channels (pkg/agent depends on pkg/channels, so channels cannot import
// agent back without a cycle). Both sides reach the same types from here —
// pkg/config imports it to type the "commands" map, pkg/agent and
// pkg/channels consume the loaded commands.
//
// The package only knows how to represent, load and expand commands; the
// discovery/precedence manager and the runtime dispatch live at higher layers.
package harness

// Source identifies where a command was discovered. Ordered by precedence in
// the loader pipeline (later sources win on name collisions), but this package
// itself does not enforce precedence — it only tags commands.
type Source string

const (
	SourceConfig    Source = "config"    // defined inline in config.json
	SourceGlobal    Source = "global"    // <LeleDir>/commands/*.md
	SourceWorkspace Source = "workspace" // <Workspace>/commands/*.md
	SourceDirectory Source = "directory" // ./.lele/commands/*.md
)

// CommandDef is the config.json / frontmatter shape of a custom command.
// It carries no identity (name comes from the map key or the file name).
type CommandDef struct {
	Description string `json:"description,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
	Template    string `json:"template"`
	AllowShell  bool   `json:"allow_shell,omitempty"`
	// AllowAbsoluteFiles is tri-state: nil inherits the harness default,
	// an explicit pointer overrides it. A pointer (not a plain bool) is
	// deliberate: the OR-semantics of AllowShell means a command cannot
	// opt *out* of a global default; absolute-file inlining must not
	// repeat that mistake.
	AllowAbsoluteFiles *bool `json:"allow_absolute_files,omitempty"`
}

// Command is a fully-resolved custom command ready for lookup and expansion.
type Command struct {
	Name        string // lowercase, no leading slash
	Description string
	Template    string
	Agent       string // agent override for the turn ("" = keep current)
	Model       string // model alias override for the turn ("" = keep current)
	AllowShell  bool   // whether !`cmd` placeholders may execute
	// AllowAbsoluteFiles is tri-state (see CommandDef): nil inherits the
	// harness default, a non-nil pointer overrides it.
	AllowAbsoluteFiles *bool
	Source             Source
	Path               string // source file ("" for config-defined commands)
}

// ToCommand builds a resolved Command from a config/frontmatter definition.
// name is the map key or the file stem; source/path tag the origin.
func (d CommandDef) ToCommand(name string, source Source, path string) *Command {
	return &Command{
		Name:               name,
		Description:        d.Description,
		Template:           d.Template,
		Agent:              d.Agent,
		Model:              d.Model,
		AllowShell:         d.AllowShell,
		AllowAbsoluteFiles: d.AllowAbsoluteFiles,
		Source:             source,
		Path:               path,
	}
}
