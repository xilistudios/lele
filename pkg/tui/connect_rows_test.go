package tui

import "testing"

func TestConnectRowsShape(t *testing.T) {
	rows := connectRows()

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

func TestConnectRowsOAuthRows(t *testing.T) {
	rows := connectRows()

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
		if want := connectOAuthCatalog[r.typ].provider; r.oauthName != want {
			t.Fatalf("OAuth row %d (%q) oauthName = %q, want catalog provider %q", i, r.typ, r.oauthName, want)
		}
		if seenOAuth[r.typ] {
			t.Fatalf("OAuth row for typ %q appears more than once", r.typ)
		}
		seenOAuth[r.typ] = true
		if i+1 >= len(rows) || rows[i+1].kind != connectRowAPIKey || rows[i+1].typ != r.typ {
			t.Fatalf("OAuth row %d (%q) must be immediately followed by its API-key row", i, r.typ)
		}
	}
	if len(seenOAuth) == 0 {
		t.Fatal("no OAuth rows emitted; connectRows() must emit one per registered OAuth preset")
	}
	for _, p := range providerPresets {
		if _, ok := connectOAuthCatalog[p.typ]; ok && !seenOAuth[p.typ] {
			t.Fatalf("preset %q is OAuth-capable but no OAuth row was emitted for it", p.typ)
		}
	}

	if row, ok := connectRowByIndex(len(rows)); ok {
		t.Fatalf("connectRowByIndex(len) = %+v, want out-of-range", row)
	}
}

func TestConnectRowsPresetsUnchanged(t *testing.T) {
	if len(providerPresets) != 34 {
		t.Fatalf("len(providerPresets) = %d, want 34 — providerPresets must NOT be modified", len(providerPresets))
	}
}

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
