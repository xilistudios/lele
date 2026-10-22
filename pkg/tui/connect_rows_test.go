package tui

import "testing"

// The connectRows() catalogue is the single source of truth for the row-first
// /connect picker. These tests pin its shape so a later change (e.g. adding
// OAuth rows) cannot silently reshape the flow the render/dispatch code
// depends on.

// TestConnectRowsShape pins the catalogue layout: two custom rows first,
// free-type last, and one API-key row per provider preset, in preset order.
// OAuth rows sit IMMEDIATELY BEFORE their matching API-key row (see
// TestConnectRowsOAuthRows), so the API-key rows are no longer contiguous
// from index 2 — the invariant below therefore tracks the API-key subsequence
// rather than assuming a fixed offset.
func TestConnectRowsShape(t *testing.T) {
	rows := connectRows()

	// Custom rows first, free-type last.
	if rows[0].kind != connectRowCustomOpenAI {
		t.Fatalf("row 0 kind = %v, want connectRowCustomOpenAI", rows[0].kind)
	}
	if rows[1].kind != connectRowCustomAnthropic {
		t.Fatalf("row 1 kind = %v, want connectRowCustomAnthropic", rows[1].kind)
	}
	last := len(rows) - 1
	if rows[last].kind != connectRowFreeType {
		t.Fatalf("last row kind = %v, want connectRowFreeType", rows[last].kind)
	}

	// Two custom + one API-key per preset + one OAuth per registered preset + free-type.
	oauthPresets := 0
	for _, p := range providerPresets {
		if _, ok := connectOAuthCatalog[p.typ]; ok {
			oauthPresets++
		}
	}
	if want := len(providerPresets) + 3 + oauthPresets; len(rows) != want {
		t.Fatalf("len(rows) = %d, want %d", len(rows), want)
	}
	if len(rows) < 30 {
		t.Fatalf("len(rows) = %d, want >= 30", len(rows))
	}

	// Every preset appears exactly once, in order, as an API-key row (the
	// OAuth rows interleaved between them are skipped by this pass).
	apiRows := make([]connectRow, 0, len(providerPresets))
	for _, r := range rows {
		if r.kind == connectRowAPIKey {
			apiRows = append(apiRows, r)
		}
	}
	if len(apiRows) != len(providerPresets) {
		t.Fatalf("got %d API-key rows, want %d", len(apiRows), len(providerPresets))
	}
	for i, r := range apiRows {
		if r.typ != providerPresets[i].typ {
			t.Fatalf("API-key row %d typ = %q, want preset %q (order must match providerPresets)", i, r.typ, providerPresets[i].typ)
		}
		if r.label != providerPresets[i].label {
			t.Fatalf("API-key row %d label = %q, want preset label %q", i, r.label, providerPresets[i].label)
		}
	}
}

// TestConnectRowsOAuthRows asserts the OAuth rows are emitted (the native
// login IS wired now) and that each one sits immediately before its matching
// API-key row, carrying the canonical registry name and nothing that would
// make the dispatch ambiguous. Non-OAuth rows must never carry OAuth data.
func TestConnectRowsOAuthRows(t *testing.T) {
	rows := connectRows()

	// Every registered providerPreset that has an OAuth catalog entry emits
	// exactly one OAuth row, immediately before the matching API-key row.
	seenOAuth := map[string]bool{}
	for i, r := range rows {
		if r.kind != connectRowOAuth {
			if r.oauthName != "" {
				t.Fatalf("row %d (%q) carries OAuth data (%q) but is not an OAuth row", i, r.label, r.oauthName)
			}
			continue
		}
		if _, ok := connectOAuthCatalog[r.typ]; !ok {
			t.Fatalf("row %d is an OAuth row for typ %q, which is not in the OAuth catalog", i, r.typ)
		}
		// connectOAuthStartFromRow rejects a row whose oauthName is empty or
		// disagrees with the catalog, so pin the agreement here: a catalog
		// rename that forgets the row would otherwise only surface as a
		// runtime "inconsistent OAuth row" instead of a red test.
		if want := connectOAuthCatalog[r.typ].provider; r.oauthName != want {
			t.Fatalf("OAuth row %d (%q) oauthName = %q, want catalog provider %q", i, r.typ, r.oauthName, want)
		}
		if seenOAuth[r.typ] {
			t.Fatalf("OAuth row for typ %q appears more than once", r.typ)
		}
		seenOAuth[r.typ] = true
		// Immediately followed by the same provider's API-key row.
		if i+1 >= len(rows) || rows[i+1].kind != connectRowAPIKey || rows[i+1].typ != r.typ {
			t.Fatalf("OAuth row %d (%q) must be immediately followed by its API-key row", i, r.typ)
		}
	}
	if len(seenOAuth) == 0 {
		t.Fatal("no OAuth rows emitted; connectRows() must emit one per registered OAuth preset")
	}
	// Every catalog preset that also has a provider preset must appear.
	for _, p := range providerPresets {
		if _, ok := connectOAuthCatalog[p.typ]; ok && !seenOAuth[p.typ] {
			t.Fatalf("preset %q is OAuth-capable but no OAuth row was emitted for it", p.typ)
		}
	}

	// connectRowByIndex refuses an out-of-range index.
	if row, ok := connectRowByIndex(len(rows)); ok {
		t.Fatalf("connectRowByIndex(len) = %+v, want out-of-range", row)
	}
}

// TestConnectRowsPresetsUnchanged guards the invariant the whole task rests
// on: providerPresets (34 entries) is untouched and the row catalogue is
// built on top of it.
func TestConnectRowsPresetsUnchanged(t *testing.T) {
	if len(providerPresets) != 34 {
		t.Fatalf("len(providerPresets) = %d, want 34 — providerPresets must NOT be modified", len(providerPresets))
	}
}

// TestConnectRowsNamesDerived: API-key-only rows derive the config key from
// the provider type (the user never types a name for them).
func TestConnectRowsNamesDerived(t *testing.T) {
	for i, r := range connectRows() {
		switch r.kind {
		case connectRowAPIKey:
			if r.name != r.typ {
				t.Fatalf("row %d (%q): name = %q, want typ %q", i, r.label, r.name, r.typ)
			}
			if r.name == "" {
				t.Fatalf("row %d: API-key row without a name", i)
			}
		case connectRowCustomOpenAI, connectRowCustomAnthropic:
			// Custom rows ask the user for the name: no derived key.
			if r.name != "" {
				t.Fatalf("row %d (%q): custom row must not pre-fill a name, got %q", i, r.label, r.name)
			}
			if r.typ == "" {
				t.Fatalf("row %d (%q): custom row must carry its provider type", i, r.label)
			}
		case connectRowFreeType:
			if r.name != "" || r.typ != "" || r.apiBase != "" {
				t.Fatalf("free-type row must be bare, got %+v", r)
			}
		}
	}
}

// TestConnectRowByIndexAndAt: the bounds helpers the picker dispatch relies
// on.
func TestConnectRowByIndexAndAt(t *testing.T) {
	rows := connectRows()
	if row, ok := connectRowByIndex(0); !ok || row.kind != connectRowCustomOpenAI {
		t.Fatalf("connectRowByIndex(0) = %+v, %v", row, ok)
	}
	if row, ok := connectRowByIndex(-1); ok {
		t.Fatalf("connectRowByIndex(-1) = %+v, want false", row)
	}
	if row, ok := connectRowByIndex(len(rows)); ok {
		t.Fatalf("connectRowByIndex(len) = %+v, want false", row)
	}
	if r := connectRowAt(len(rows)); r != nil {
		t.Fatalf("connectRowAt(len) = %+v, want nil", r)
	}
	if r := connectRowAt(0); r == nil || r.kind != connectRowCustomOpenAI {
		t.Fatalf("connectRowAt(0) = %+v, want the custom OpenAI row", r)
	}
}
