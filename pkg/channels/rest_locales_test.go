package channels

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/locales"
)

func newLocalesTestChannel(t *testing.T) (*NativeChannel, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	n := &NativeChannel{leleDir: dir}

	tuiPack := map[string]string{"tui.title": "Lele", "tui.welcome": "Bienvenue"}
	webPack := map[string]any{"chat": map[string]any{"newChat": "Nouveau"}}
	catalog := locales.Catalog{
		Version: 1,
		Languages: []locales.Language{
			{Code: "es", Name: "Spanish", NativeName: "Español", Builtin: true},
			{Code: "en", Name: "English", NativeName: "English", Builtin: true},
			{Code: "pt", Name: "Portuguese", NativeName: "Português", Builtin: true},
			{Code: "fr", Name: "French", NativeName: "Français"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xilistudios/lele/main/locales/tui/fr.json":
			_ = json.NewEncoder(w).Encode(tuiPack)
		case "/xilistudios/lele/main/locales/web/fr.json":
			_ = json.NewEncoder(w).Encode(webPack)
		case "/xilistudios/lele/main/locales/index.json":
			_ = json.NewEncoder(w).Encode(catalog)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	mgr := locales.NewManagerWithCacheDir(locales.JoinCacheDir(dir))
	mgr.SetHTTPClient(&http.Client{
		Transport: rewriteHostForTest{base: srv.URL},
	})
	n.localesMgr = mgr
	return n, srv
}

type rewriteHostForTest struct {
	base string
}

func (r rewriteHostForTest) RoundTrip(req *http.Request) (*http.Response, error) {
	base, err := http.NewRequest(http.MethodGet, r.base, nil)
	if err != nil {
		return nil, err
	}
	u := *req.URL
	u.Scheme = base.URL.Scheme
	u.Host = base.URL.Host
	req2 := req.Clone(req.Context())
	req2.URL = &u
	req2.Host = u.Host
	return http.DefaultTransport.RoundTrip(req2)
}

func TestHandleLocalesList(t *testing.T) {
	n, _ := newLocalesTestChannel(t)
	n.agentLoop = nil

	req := httptest.NewRequest(http.MethodGet, "/api/v1/locales", nil)
	rr := httptest.NewRecorder()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("handleLocalesList panicked: %v", r)
			}
		}()
		n.handleLocalesList(rr, req)
	}()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp LocalesListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Languages) < 3 {
		t.Fatalf("languages = %d, want >= 3", len(resp.Languages))
	}
	if resp.CacheDir == "" {
		t.Error("cache_dir empty")
	}
}

func TestHandleLocaleInstallAndGet(t *testing.T) {
	n, _ := newLocalesTestChannel(t)
	n.agentLoop = nil

	req := httptest.NewRequest(http.MethodPost, "/api/v1/locales/fr/install", nil)
	req.SetPathValue("code", "fr")
	rr := httptest.NewRecorder()
	n.handleLocaleInstall(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("install status = %d body=%s", rr.Code, rr.Body.String())
	}
	var inst LocaleInstallResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &inst); err != nil {
		t.Fatal(err)
	}
	if !inst.Installed || inst.Code != "fr" {
		t.Fatalf("install response = %+v", inst)
	}

	if _, err := os.Stat(filepath.Join(n.getLocalesManager().CacheDir(), "web", "fr.json")); err != nil {
		t.Fatalf("web pack missing: %v", err)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/locales/fr", nil)
	req2.SetPathValue("code", "fr")
	rr2 := httptest.NewRecorder()
	n.handleLocaleGet(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", rr2.Code, rr2.Body.String())
	}
	var pack LocalePackResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &pack); err != nil {
		t.Fatal(err)
	}
	if pack.Source != "cache" {
		t.Errorf("source = %q, want cache", pack.Source)
	}

	req3 := httptest.NewRequest(http.MethodDelete, "/api/v1/locales/fr", nil)
	req3.SetPathValue("code", "fr")
	rr3 := httptest.NewRecorder()
	n.handleLocaleUninstall(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("uninstall status = %d", rr3.Code)
	}

	req4 := httptest.NewRequest(http.MethodDelete, "/api/v1/locales/en", nil)
	req4.SetPathValue("code", "en")
	rr4 := httptest.NewRecorder()
	n.handleLocaleUninstall(rr4, req4)
	if rr4.Code == http.StatusOK {
		t.Error("builtin uninstall should fail")
	}
}

func TestHandleLocaleGetBuiltin(t *testing.T) {
	n, _ := newLocalesTestChannel(t)
	n.agentLoop = nil
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locales/en", nil)
	req.SetPathValue("code", "en")
	rr := httptest.NewRecorder()
	n.handleLocaleGet(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var pack LocalePackResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &pack); err != nil {
		t.Fatal(err)
	}
	if pack.Source != "builtin" {
		t.Errorf("source = %q, want builtin", pack.Source)
	}
}
