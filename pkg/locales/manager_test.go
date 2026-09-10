package locales

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m := NewManagerWithCacheDir(dir)
	return m
}

func TestNormalizeCode(t *testing.T) {
	cases := map[string]string{
		"EN":        "en",
		" pt-BR ":   "pt",
		"fr-FR":     "fr",
		"zh-CN":     "zh",
		"español":   "es",
		"Português": "pt",
		"ja":        "ja",
	}
	for in, want := range cases {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsBuiltin(t *testing.T) {
	if !isBuiltin("es") || !isBuiltin("en") || !isBuiltin("pt") {
		t.Fatal("expected es/en/pt to be builtin")
	}
	if isBuiltin("fr") {
		t.Fatal("fr must not be builtin")
	}
}

func TestDefaultCatalogHasBuiltins(t *testing.T) {
	m := testManager(t)
	cat := m.Catalog()
	if len(cat.Languages) < 3 {
		t.Fatalf("catalog languages = %d, want >= 3", len(cat.Languages))
	}
	codes := map[string]bool{}
	for _, l := range cat.Languages {
		codes[l.Code] = true
	}
	for _, b := range Builtins {
		if !codes[b] {
			t.Errorf("missing builtin %s in default catalog", b)
		}
	}
}

func TestListMarksBuiltinsInstalled(t *testing.T) {
	m := testManager(t)
	list := m.List(context.Background())
	found := map[string]Status{}
	for _, s := range list {
		found[s.Code] = s
	}
	for _, b := range Builtins {
		s, ok := found[b]
		if !ok {
			t.Fatalf("builtin %s missing from List", b)
		}
		if !s.Installed || !s.Builtin {
			t.Errorf("builtin %s: installed=%v builtin=%v, want both true", b, s.Installed, s.Builtin)
		}
	}
}

func TestInstallAndUninstall(t *testing.T) {
	m := testManager(t)

	tuiPack := map[string]string{"tui.title": "Lele", "tui.welcome": "Bienvenue!"}
	webPack := map[string]any{"chat": map[string]any{"newChat": "Nouveau"}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xilistudios/lele/main/locales/tui/fr.json":
			_ = json.NewEncoder(w).Encode(tuiPack)
		case "/xilistudios/lele/main/locales/web/fr.json":
			_ = json.NewEncoder(w).Encode(webPack)
		case "/xilistudios/lele/main/locales/index.json":
			_ = json.NewEncoder(w).Encode(Catalog{
				Version: 1,
				Languages: []Language{
					{Code: "es", Name: "Spanish", NativeName: "Español", Builtin: true},
					{Code: "en", Name: "English", NativeName: "English", Builtin: true},
					{Code: "pt", Name: "Portuguese", NativeName: "Português", Builtin: true},
					{Code: "fr", Name: "French", NativeName: "Français"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Point raw URL host at the test server by rewriting via custom transport
	// is overkill — override raw URL by swapping repo path through a reverse
	// proxy style client. We use a custom RoundTripper that redirects.
	m.client = &http.Client{
		Timeout: 5 * time.Second,
		Transport: rewriteHost{base: srv.URL},
	}

	ctx := context.Background()
	if err := m.Install(ctx, "fr"); err != nil {
		t.Fatalf("Install(fr): %v", err)
	}
	if !m.IsInstalled("fr") {
		t.Fatal("fr should be installed")
	}

	tui, err := m.GetTUI("fr", nil)
	if err != nil {
		t.Fatalf("GetTUI(fr): %v", err)
	}
	if tui["tui.welcome"] != "Bienvenue!" {
		t.Errorf("tui.welcome = %q", tui["tui.welcome"])
	}

	web, err := m.GetWeb("fr")
	if err != nil {
		t.Fatalf("GetWeb(fr): %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(web, &parsed); err != nil {
		t.Fatalf("web JSON: %v", err)
	}

	// Catalog refresh should mark fr listed + installed.
	list := m.List(ctx)
	var fr *Status
	for i := range list {
		if list[i].Code == "fr" {
			fr = &list[i]
		}
	}
	if fr == nil {
		t.Fatal("fr missing from List after install")
	}
	if !fr.Installed {
		t.Error("fr should be Installed=true")
	}

	if err := m.Uninstall("fr"); err != nil {
		t.Fatalf("Uninstall(fr): %v", err)
	}
	if m.IsInstalled("fr") {
		t.Error("fr should not be installed after Uninstall")
	}
	if err := m.Uninstall("en"); err == nil {
		t.Error("Uninstall(en) should fail for builtin")
	}
}

func TestInstallRejectsEmptyCode(t *testing.T) {
	m := testManager(t)
	if err := m.Install(context.Background(), "  "); err == nil {
		t.Fatal("expected error for empty code")
	}
}

func TestInstallBuiltinIsNoop(t *testing.T) {
	m := testManager(t)
	if err := m.Install(context.Background(), "es"); err != nil {
		t.Fatalf("Install(es) builtin: %v", err)
	}
}

func TestGetTUIBuiltinRequiresEmbedded(t *testing.T) {
	m := testManager(t)
	if _, err := m.GetTUI("en", nil); err == nil {
		t.Fatal("expected error when builtin not in embedded map")
	}
	tui, err := m.GetTUI("en", map[string]map[string]string{
		"en": {"tui.title": "Lele"},
	})
	if err != nil || tui["tui.title"] != "Lele" {
		t.Fatalf("embedded builtin lookup failed: %v %v", tui, err)
	}
}

func TestDisplayLabel(t *testing.T) {
	l := Language{Code: "fr", NativeName: "Français"}
	if got := DisplayLabel(l); got != "Français (fr)" {
		t.Errorf("DisplayLabel = %q", got)
	}
}

func TestSortLanguagesBuiltinsFirst(t *testing.T) {
	langs := []Language{
		{Code: "ja", NativeName: "日本語"},
		{Code: "en", NativeName: "English", Builtin: true},
		{Code: "fr", NativeName: "Français"},
		{Code: "es", NativeName: "Español", Builtin: true},
		{Code: "pt", NativeName: "Português", Builtin: true},
	}
	SortLanguages(langs)
	want := []string{"es", "en", "pt", "fr", "ja"}
	for i, w := range want {
		if langs[i].Code != w {
			t.Fatalf("order[%d] = %s, want %s (full=%v)", i, langs[i].Code, w, langs)
		}
	}
}

// rewriteHost sends raw.githubusercontent.com requests to a local test server.
type rewriteHost struct {
	base string
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	base, err := http.NewRequest(http.MethodGet, r.base, nil)
	if err != nil {
		return nil, err
	}
	u.Scheme = base.URL.Scheme
	u.Host = base.URL.Host
	req2 := req.Clone(req.Context())
	req2.URL = &u
	req2.Host = u.Host
	return http.DefaultTransport.RoundTrip(req2)
}

func TestInstalledCodesReadsDisk(t *testing.T) {
	m := testManager(t)
	if err := os.MkdirAll(filepath.Join(m.cacheDir, "tui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(m.cacheDir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Incomplete (tui only) — must not count.
	_ = os.WriteFile(m.TUIPath("de"), []byte(`{"a":"b"}`), 0o644)
	// Complete.
	_ = os.WriteFile(m.TUIPath("fr"), []byte(`{"a":"b"}`), 0o644)
	_ = os.WriteFile(m.WebPath("fr"), []byte(`{"a":{"b":"c"}}`), 0o644)

	codes := m.InstalledCodes()
	if len(codes) != 1 || codes[0] != "fr" {
		t.Fatalf("InstalledCodes = %v, want [fr]", codes)
	}
}
