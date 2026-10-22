// mcp_add_form.go implements the pure step/validation/entry-building layer
// and the bespoke modal renderer of the "/mcp → n" MCP-add wizard.
//
// The wizard reuses the generic form STATE (formStepIndex, formValues,
// formError) but not the generic renderer: the step list is kind-dependent
// (args is stdio-only, type/headers are remote-only), so steps are keyed by
// a fixed set of SLOT constants and mcpAddVisibleSlots maps a kind to the
// slot order the wizard walks. renderFormSteps assumes steps[i] ↔
// formValues[i] ↔ formStepIndex, which breaks with a dynamic step set.
//
// Secrecy rule (plan D9): env/header VALUES never reach any persisted or
// rendered surface — completed steps and the review block mask them through
// mcpMaskPairs, and every pair-parser error is verb-free and value-free. The
// ONLY place a value is on screen is the live single-line text input at the
// env/headers step, i.e. the user's own in-flight keystroke.
//
// Everything here is unexported and side-effect free: no disk access
// (mcpAddReviewWarnings reads m.mcpInventory, the snapshot the visible rows
// were built from), no ${VAR} expansion, no type defaulting (the mcp.json
// READER defaults type to http; the writer keeps the object minimal).

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// ── Slots ─────────────────────────────────────────────────────────────

// The wizard has a fixed set of ten value slots (formValues indexes, one per
// field). Which slots are VISIBLE depends on the chosen transport kind; the
// review slot is always last.
const (
	mcpAddStepName        = iota // 0: trimmed raw
	mcpAddStepKind               // 1: canonical stdio|remote
	mcpAddStepEndpoint           // 2: command (stdio) | url (remote)
	mcpAddStepArgs               // 3: stdio only — raw comma-separated text
	mcpAddStepType               // 4: remote only — ""|http|sse (trimmed raw)
	mcpAddStepEnv                // 5: KEY=VALUE,… (trimmed raw)
	mcpAddStepHeaders            // 6: remote only — KEY=VALUE,… (trimmed raw)
	mcpAddStepDescription        // 7: trimmed raw
	mcpAddStepLayer              // 8: canonical global|agent|project
	mcpAddStepReview             // 9: yes/no, NOT stored
	mcpAddStepCount              // number of slots (formValues length)
)

// mcpAddVisibleSlots returns the slot indexes the wizard walks for one
// transport kind, in step order: name → kind → command|url → args|type →
// env → headers(remote) → description → layer → review. "" means the kind
// has not been chosen yet, in which case only name and kind are shown.
func mcpAddVisibleSlots(kind string) []int {
	switch kind {
	case "stdio":
		return []int{mcpAddStepName, mcpAddStepKind, mcpAddStepEndpoint,
			mcpAddStepArgs, mcpAddStepEnv, mcpAddStepDescription,
			mcpAddStepLayer, mcpAddStepReview}
	case "remote":
		return []int{mcpAddStepName, mcpAddStepKind, mcpAddStepEndpoint,
			mcpAddStepType, mcpAddStepEnv, mcpAddStepHeaders,
			mcpAddStepDescription, mcpAddStepLayer, mcpAddStepReview}
	default:
		return []int{mcpAddStepName, mcpAddStepKind}
	}
}

// mcpAddTransport returns the canonical transport kind stored in slot 1
// ("stdio"/"remote"), or "" while the kind is unset or unparsable.
func (m *Model) mcpAddTransport() string {
	if len(m.formValues) <= mcpAddStepKind {
		return ""
	}
	switch m.formValues[mcpAddStepKind] {
	case "stdio", "remote":
		return m.formValues[mcpAddStepKind]
	}
	return ""
}

// ── Step labels & placeholders ────────────────────────────────────────

// mcpAddStepLabel returns the localized label of one slot. mcpAddStepKind
// names what the wizard asks ("Transport"), not the /mcp filter chips.
func mcpAddStepLabel(slot int) string {
	switch slot {
	case mcpAddStepName:
		return i18n.T("tui.mcpAddStepName")
	case mcpAddStepKind:
		return i18n.T("tui.mcpAddStepKind")
	case mcpAddStepEndpoint:
		// Kind-dependent: "Command (stdio)" or "URL (remote)". The wizard
		// never shows the endpoint before the kind is chosen, so the stdio
		// label is the neutral default.
		return i18n.T("tui.mcpAddStepCommand")
	case mcpAddStepArgs:
		return i18n.T("tui.mcpAddStepArgs")
	case mcpAddStepType:
		return i18n.T("tui.mcpAddStepType")
	case mcpAddStepEnv:
		return i18n.T("tui.mcpAddStepEnv")
	case mcpAddStepHeaders:
		return i18n.T("tui.mcpAddStepHeaders")
	case mcpAddStepDescription:
		return i18n.T("tui.mcpAddStepDescription")
	case mcpAddStepLayer:
		return i18n.T("tui.mcpAddStepLayer")
	case mcpAddStepReview:
		return i18n.T("tui.mcpAddStepReview")
	}
	return ""
}

// mcpAddEndpointLabel returns the kind-dependent label of the shared
// command/url slot ("Command (stdio)" or "URL (remote)").
func (m *Model) mcpAddEndpointLabel() string {
	if m.mcpAddTransport() == "remote" {
		return i18n.T("tui.mcpAddStepURL")
	}
	return i18n.T("tui.mcpAddStepCommand")
}

// mcpAddStepPlaceholder returns the localized placeholder of one slot for
// the wizard's current transport kind (the command/url and args/type slots
// are kind-dependent).
func (m *Model) mcpAddStepPlaceholder(slot int) string {
	switch slot {
	case mcpAddStepName:
		return i18n.T("tui.mcpAddNamePlaceholder")
	case mcpAddStepKind:
		return i18n.T("tui.mcpAddKindPlaceholder")
	case mcpAddStepEndpoint:
		if m.mcpAddTransport() == "remote" {
			return i18n.T("tui.mcpAddURLPlaceholder")
		}
		return i18n.T("tui.mcpAddCommandPlaceholder")
	case mcpAddStepArgs:
		return i18n.T("tui.mcpAddArgsPlaceholder")
	case mcpAddStepType:
		return i18n.T("tui.mcpAddTypePlaceholder")
	case mcpAddStepEnv:
		return i18n.T("tui.mcpAddEnvPlaceholder")
	case mcpAddStepHeaders:
		return i18n.T("tui.mcpAddHeadersPlaceholder")
	case mcpAddStepDescription:
		return i18n.T("tui.mcpAddDescriptionPlaceholder")
	case mcpAddStepLayer:
		return i18n.T("tui.mcpAddLayerPlaceholder")
	case mcpAddStepReview:
		return i18n.T("tui.mcpAddConfirmPlaceholder")
	}
	return ""
}

// mcpAddStepNames returns the localized labels of exactly the visible slots
// for the wizard's current transport, in step order. It is what
// formStepNames() delegates to for ModalMCPAdd (single source of truth for
// the labels, even though the renderer is bespoke).
func (m *Model) mcpAddStepNames() []string {
	slots := mcpAddVisibleSlots(m.mcpAddTransport())
	names := make([]string, 0, len(slots))
	for _, slot := range slots {
		label := mcpAddStepLabel(slot)
		// The shared command/url slot has a kind-dependent label; mirror the
		// renderer so the label set matches what the user sees.
		if slot == mcpAddStepEndpoint {
			label = m.mcpAddEndpointLabel()
		}
		names = append(names, label)
	}
	return names
}

// ── Per-slot parsers ──────────────────────────────────────────────────
//
// Every parser takes trimmed-or-raw text as typed and returns the canonical
// value or the localized refusal. Error messages are value-free by
// construction: a pair's error never echoes the text after '=' (that half
// holds the value and values may be secrets), and a pair with no '=' at all
// is not echoed either.

// mcpAddValidateName enforces the entry-name rule of pkg/mcp's writer
// (validEntryName): non-empty, no '/'. Spaces are legal (the CLI allows
// them); the name is a map key, not a path.
func mcpAddValidateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrNameRequired"))
	case strings.Contains(name, "/"):
		return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrNameSlash"))
	}
	return name, nil
}

// mcpAddParseTransport normalizes the kind input to the canonical
// "stdio"/"remote". Shortcuts mirror the CLI's flag-less ergonomics:
// stdio|s|1, remote|r|2 (case-insensitive). Empty or anything else is
// refused — there is no default kind: the wizard must ask.
func mcpAddParseTransport(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "stdio", "s", "1":
		return "stdio", nil
	case "remote", "r", "2":
		return "remote", nil
	}
	return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrKind"))
}

// mcpAddParseEndpoint validates the shared command/url slot: exactly one of
// the two MUST be set (ServerConfig.validate refuses entries with both or
// neither), and the wizard's single slot makes "both" structurally
// impossible. The value is never mutated — a ${VAR} survives verbatim.
func mcpAddParseEndpoint(kind, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		if kind == "remote" {
			return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrURL"))
		}
		return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrCommand"))
	}
	return v, nil
}

// mcpAddParseArgs splits the args text on ',' ONLY — never on whitespace:
// paths and globs may contain spaces ("--flag=/home/my user/notes") and the
// CLI spells args as repeatable --arg flags, so commas are the one
// unambiguous UI separator. Each segment is trimmed, empty segments are
// dropped, and empty input yields nil (the field is omitted from the
// written object). This parser cannot fail.
func mcpAddParseArgs(raw string) []string {
	var out []string
	for _, seg := range strings.Split(raw, ",") {
		if seg = strings.TrimSpace(seg); seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

// mcpAddParseType normalizes the remote transport type: empty stays empty
// (the mcp.json READER defaults it to http; the writer keeps the object
// minimal, so an untouched type is never written), http|sse are lowercased
// regardless of how they were typed, anything else is refused.
func mcpAddParseType(raw string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "":
		return "", nil
	case "http", "sse":
		return v, nil
	}
	return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrType"))
}

// mcpAddParsePairs turns "KEY=VALUE,KEY=VALUE,…" into a map. Splits on ',',
// each segment on the FIRST '=' (a header value may itself contain '=':
// "Authorization=Bearer abc=def"). "KEY=" (empty value) is allowed; a
// segment without '=' or with an empty KEY is refused. Returns nil for
// empty input or all-empty segments so the field stays omitted from the
// written object. The returned error NEVER contains the text after '='.
func mcpAddParsePairs(raw string) (map[string]string, error) {
	var out map[string]string
	for _, seg := range strings.Split(raw, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		eq := strings.IndexByte(seg, '=')
		if eq < 0 {
			return nil, fmt.Errorf("%s", i18n.T("tui.mcpAddErrPairNoEquals"))
		}
		// Trim BOTH halves: the wizard is a free-text field typed without
		// completion, so " TOKEN = abc " must not silently store a key with
		// a trailing space (the variable would never match at spawn time)
		// or a value with a leading space (review #2 MINOR 5). Values keep
		// their INNER text verbatim — "${VAR}" and "Bearer x=y" survive.
		// A duplicated KEY is last-wins, like the CLI's mcpPairs.
		key := strings.TrimSpace(seg[:eq])
		if key == "" {
			return nil, fmt.Errorf("%s", i18n.T("tui.mcpAddErrPairNoKey"))
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[key] = strings.TrimSpace(seg[eq+1:])
	}
	return out, nil
}

// mcpMaskPairs masks the VALUE of every "KEY=VALUE" segment for display:
// "A=s3cr3t,B=" → "A=•••••,B=". A segment without '=' passes through
// verbatim (it is malformed input being echoed back, nothing to hide), and
// the value is never leaked — the same secrecy rule the /mcp detail screen
// follows with key-name-only env/header lists. A segment with an EMPTY
// value ("KEY=") is already fully redacted and is left untouched, so the
// summary never gains characters the user never typed. A real value is
// replaced by five bullets — the same 5-bullet budget used for secrets —
// and the surrounding whitespace is preserved verbatim so the masked line
// still lines up with what was typed.
func mcpMaskPairs(raw string) string {
	segs := strings.Split(raw, ",")
	for i, seg := range segs {
		eq := strings.IndexByte(seg, '=')
		if eq < 0 || eq == len(seg)-1 {
			continue // no '=', or KEY= (empty value): nothing to hide
		}
		prefix := seg[:eq+1]
		suffix := seg[eq+1:]
		// Preserve any whitespace that FOLLOWED the value ("A=1 " →
		// "A=••••• "), so masking never reflows the line.
		trimEnd := strings.TrimRight(suffix, " \t")
		segs[i] = prefix + strings.Repeat("•", 5) + suffix[len(trimEnd):]
	}
	return strings.Join(segs, ",")
}

// mcpAddParseLayer normalizes the target layer: empty means the CLI default
// (global), the canonical names (global|agent|project) and the localized
// /mcp filter labels (i18n.T tui.mcpFilterGlobal/…Agent/…Project) are all
// accepted case-insensitively — the filter chips are what the user last saw
// on screen, so they must type-check. Anything else is refused.
func mcpAddParseLayer(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "":
		return mcp.LayerGlobal, nil
	case mcp.LayerGlobal, mcp.LayerAgent, mcp.LayerProject:
		return v, nil
	}
	for _, layer := range mcp.Layers() {
		switch layer {
		case mcp.LayerGlobal:
			if v == strings.ToLower(i18n.T("tui.mcpFilterGlobal")) {
				return layer, nil
			}
		case mcp.LayerAgent:
			if v == strings.ToLower(i18n.T("tui.mcpFilterAgent")) {
				return layer, nil
			}
		case mcp.LayerProject:
			if v == strings.ToLower(i18n.T("tui.mcpFilterProject")) {
				return layer, nil
			}
		}
	}
	return "", fmt.Errorf("%s", i18n.T("tui.mcpAddErrLayer"))
}

// mcpAddLayerRank (index of a canonical layer in mcp.Layers(), -1 when
// unknown) is defined in mcp_add.go next to its write-path consumer.

// ── Entry building ────────────────────────────────────────────────────

// mcpAddEntry builds the mcp.ServerConfig for the confirmed wizard values.
// It mirrors the CLI's mcpAddArgs.entry(): raw trimmed values ONLY — no
// ${VAR} expansion (the reader expands, the writer stores what was typed)
// and no type defaulting (an empty type is omitted from the object).
//
// Field isolation: Command+Args only for stdio, URL+Type+Headers only for
// remote, Env for both, Description trimmed, Disabled=false, Auth=nil (the
// OAuth block is out of the wizard's v1 scope — see tui.mcpAddReviewAuthHint).
// Returns the FIRST parse error, localized by the slot's parser.
func (m *Model) mcpAddEntry() (mcp.ServerConfig, error) {
	val := func(slot int) string {
		if slot < len(m.formValues) {
			return m.formValues[slot]
		}
		return ""
	}

	var cfg mcp.ServerConfig

	// Both kinds: env.
	env, err := mcpAddParsePairs(val(mcpAddStepEnv))
	if err != nil {
		return mcp.ServerConfig{}, err
	}
	cfg.Env = env

	switch m.mcpAddTransport() {
	case "remote":
		endpoint, err := mcpAddParseEndpoint("remote", val(mcpAddStepEndpoint))
		if err != nil {
			return mcp.ServerConfig{}, err
		}
		typ, err := mcpAddParseType(val(mcpAddStepType))
		if err != nil {
			return mcp.ServerConfig{}, err
		}
		headers, err := mcpAddParsePairs(val(mcpAddStepHeaders))
		if err != nil {
			return mcp.ServerConfig{}, err
		}
		cfg.URL = endpoint
		cfg.Type = typ
		cfg.Headers = headers
	case "stdio":
		endpoint, err := mcpAddParseEndpoint("stdio", val(mcpAddStepEndpoint))
		if err != nil {
			return mcp.ServerConfig{}, err
		}
		cfg.Command = endpoint
		cfg.Args = mcpAddParseArgs(val(mcpAddStepArgs))
	default:
		// Unreachable through the walk (an unparsable kind never leaves the
		// kind slot), but mcpAddEntry is also called directly: refuse
		// instead of silently guessing a transport for an unknown kind.
		return mcp.ServerConfig{}, fmt.Errorf("%s", i18n.T("tui.mcpAddErrKind"))
	}

	cfg.Description = strings.TrimSpace(val(mcpAddStepDescription))
	// Disabled=false and Auth=nil are the zero values and stay unset on
	// purpose: UpsertEntry omits them from the written object.
	return cfg, nil
}

// ── Review warnings ───────────────────────────────────────────────────

// mcpAddReviewWarnings resolves the review step's advisory lines from
// m.mcpInventory — the EXACT snapshot the visible /mcp rows were built
// from, never a fresh disk read, so the review screen can never disagree
// with the rows the user just saw.
//
// With no usable snapshot (!mcpInventoryValid or an unresolvable target
// layer) there is nothing to compare against and the review is silent: the
// write path re-reads and re-validates authoritatively anyway.
//
//	name already in the target layer → replacing warning (confirming overwrites)
//	copy in a HIGHER layer           → shadowed warning (this entry will not take effect)
//	copy in a LOWER layer            → note (this entry shadows that copy)
func (m *Model) mcpAddReviewWarnings() []string {
	if !m.mcpInventoryValid {
		return nil
	}
	// The name step always stores a validated name; review comes after it.
	name := strings.TrimSpace(m.formValue(mcpAddStepName))
	if name == "" {
		return nil
	}
	// The layer step stores the canonical layer; before it is reached the
	// text may still be raw, so parse it with the layer step's semantics.
	// An unparsable/unknown layer resolves to global's rank via
	// mcpAddLayerRank's -1 fallback, matching the empty-input default.
	target := mcp.LayerGlobal
	if layer, err := mcpAddParseLayer(m.formValue(mcpAddStepLayer)); err == nil {
		target = layer
	}
	rank := mcpAddLayerRank(target)
	if rank < 0 {
		return nil
	}

	var warnings []string
	for _, row := range m.mcpInventory.ByName[name] {
		rowRank := mcpAddLayerRank(row.Layer)
		if rowRank < 0 {
			continue
		}
		switch {
		case row.Layer == target:
			warnings = append(warnings, fmt.Sprintf(i18n.T("tui.mcpAddReviewWarnReplace"), name, target))
		case rowRank > rank:
			// Every upper-layer copy shadows the write, so each one is
			// named — the list is bounded by the layer count, and the
			// post-write feedback reports only the highest (mcpAddShadowLayer).
			warnings = append(warnings, fmt.Sprintf(i18n.T("tui.mcpAddReviewWarnShadowed"), row.Layer))
		case rowRank < rank:
			warnings = append(warnings, fmt.Sprintf(i18n.T("tui.mcpAddReviewNoteShadows"), row.Layer))
		}
	}
	return warnings
}

// formValue returns the stored value of one slot ("" when unset). The
// generic form state predates the wizard and is slice-based, so every read
// is bounds-checked.
func (m *Model) formValue(slot int) string {
	if slot < len(m.formValues) {
		return m.formValues[slot]
	}
	return ""
}

// ── Renderer ──────────────────────────────────────────────────────────

// renderMCPAddModal paints the MCP-add wizard. It reuses the established
// modal chrome (title, step glyphs, error line, input bar, hints, frame)
// but walks mcpAddVisibleSlots instead of the step list verbatim: the
// generic renderFormSteps assumes steps[i] ↔ formValues[i], which breaks
// with the kind-dependent slot set.
func (m *Model) renderMCPAddModal() string {
	var sb strings.Builder
	sb.WriteString(TitleStyle.Render(i18n.T("tui.mcpAdd")) + "\n\n")
	sb.WriteString(m.renderMCPAddSteps())
	sb.WriteString("\n")

	if m.formError != "" {
		sb.WriteString(lipgloss.NewStyle().Foreground(PrimaryColor).Render("  ✗ "+m.formError) + "\n\n")
	}

	// The review step is identified through the slot indirection too: the
	// cursor is a POSITION, the review marker is a SLOT constant.
	if m.mcpAddCurrentSlot() == mcpAddStepReview {
		sb.WriteString(m.renderMCPAddReview())
	} else {
		// The live input is masked at the env/headers steps by
		// textInputView (secret_fields.go) — the only place a value is on
		// screen, and only while the user is typing it.
		m.textInput.Width = 40
		sb.WriteString(InputBarContainer.Width(44).Render(m.textInputView()) + "\n\n")
		sb.WriteString(HelpStyle.Render("  "+i18n.T("tui.mcpAddHints")) + "\n")
	}

	return m.paintFrame(ModalContainer.Render(sb.String()))
}

// renderMCPAddSteps renders the visible slots with the established glyphs:
// completed "  ✓ label: value" (ModalItemInactive), current
// "  ▶ label: [value]" (ModalItemActive, "…" while empty), pending
// "  ○ label" (CommentColorStyle). Env and header VALUES are masked through
// mcpMaskPairs on every completed line — the secrecy rule has no exceptions
// for summaries.
// formStepIndex and formValues are SHARED across every generic wizard in the
// TUI (mcp_auth.go, commands_admin_write.go, ...), so this
// renderer MUST NOT trust the raw index against the step POSITION: the cursor
// is a slot constant (formValues is indexed by slot), so a slot that is not
// in the current kind's visible set (e.g. a stdio run leaving args filled)
// must never render as "current". mcpAddCurrentSlot validates the cursor
// against mcpAddVisibleSlots; without that check a stale slot from a
// previous flow renders with all steps marked complete.
func (m *Model) renderMCPAddSteps() string {
	var sb strings.Builder
	// The cursor is a SLOT constant (formValues is indexed by slot), so it
	// is compared against the visible set's members, not against the loop
	// position — that is what mcpAddCurrentSlot() resolves below.
	slots := mcpAddVisibleSlots(m.mcpAddTransport())
	current := m.mcpAddCurrentSlot() // resolves the cursor through the slot set
	for i := 0; i < len(slots); i++ {
		slot := slots[i]
		label := mcpAddStepLabel(slot)
		if slot == mcpAddStepEndpoint {
			label = m.mcpAddEndpointLabel()
		}
		switch {
		case slot < current:
			val := m.mcpAddStepDisplayValue(slot)
			sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  ✓ %s: %s", label, val)) + "\n")
		case slot == current:
			val := m.textInput.Value()
			if slot == mcpAddStepEnv || slot == mcpAddStepHeaders {
				val = mcpMaskPairs(val)
			}
			if val == "" {
				val = "…"
			}
			sb.WriteString(ModalItemActive.Render(fmt.Sprintf("  ▶ %s: [%s]", label, val)) + "\n")
		default:
			sb.WriteString(CommentColorStyle.Render("  ○ "+label) + "\n")
		}
	}
	return sb.String()
}

// mcpAddStepDisplayValue renders one COMPLETED slot's stored value: the
// shared endpoint slot shows its kind-dependent label's value as typed, the
// pair slots are masked, the args slot renders its parsed count (the raw
// text is just the joined segments), everything else renders verbatim, and
// empty values render the localized tui.mcpDetailNone "(none)".
func (m *Model) mcpAddStepDisplayValue(slot int) string {
	raw := m.formValue(slot)
	var val string
	switch slot {
	case mcpAddStepEnv, mcpAddStepHeaders:
		val = mcpMaskPairs(raw)
	case mcpAddStepArgs:
		if n := len(mcpAddParseArgs(raw)); n > 0 {
			val = fmt.Sprintf("%d", n)
		}
	default:
		val = raw
	}
	val = strings.TrimSpace(val)
	if val == "" {
		return i18n.T("tui.mcpDetailNone")
	}
	return val
}

// renderMCPAddReview renders the review block: one "label: value" line per
// applicable slot (env/header pairs masked, args by count), the advisory
// lines resolved from the inventory snapshot, and the OAuth-out-of-scope
// hint. It deliberately never serializes the entry: a json.Marshal of the
// ServerConfig would put env/header values on screen.
func (m *Model) renderMCPAddReview() string {
	var sb strings.Builder
	for _, slot := range mcpAddVisibleSlots(m.mcpAddTransport()) {
		if slot == mcpAddStepReview || slot == mcpAddStepKind {
			continue // the confirm slot is the input itself; the chosen kind is implied by the fields below
		}
		label := mcpAddStepLabel(slot)
		if slot == mcpAddStepEndpoint {
			label = m.mcpAddEndpointLabel()
		}
		sb.WriteString(ModalItemInactive.Render(fmt.Sprintf("  %s: %s", label, m.mcpAddStepDisplayValue(slot))) + "\n")
	}
	for _, w := range m.mcpAddReviewWarnings() {
		sb.WriteString(lipgloss.NewStyle().Foreground(PrimaryColor).Render("  "+w) + "\n")
	}
	sb.WriteString("\n" + CommentColorStyle.Render("  "+i18n.T("tui.mcpAddReviewAuthHint")) + "\n")
	sb.WriteString("\n" + HelpStyle.Render("  "+i18n.T("tui.mcpAddReviewHints")) + "\n")
	return sb.String()
}

// mcpAddCurrentSlot resolves the wizard slot the cursor is on. The cursor
// IS the slot constant (formStepIndex stores the slot, never a position
// inside the visible-slot set), because formValues is indexed BY SLOT — a
// position cursor would silently misread formValues[position] for the
// kind-dependent sets, where slot != position (stdio skips type, remote
// skips args). Values stay within the visible set in practice; the
// out-of-range guard clamps to the last visible slot (review) so a stale
// larger index from a previous flow renders review rather than nothing.
func (m *Model) mcpAddCurrentSlot() int {
	slots := mcpAddVisibleSlots(m.mcpAddTransport())
	last := slots[len(slots)-1]
	for _, slot := range slots {
		if slot == m.formStepIndex {
			return slot
		}
	}
	if m.formStepIndex < slots[0] {
		return slots[0]
	}
	return last
}
