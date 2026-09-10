package locales

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager downloads, caches, and serves language packs.
type Manager struct {
	repo   string
	branch string
	client *http.Client
	// cacheDir is ~/.lele/locales (or override for tests).
	cacheDir string

	mu       sync.RWMutex
	catalog  *Catalog
	catalogT time.Time
}

// DefaultLeleDir mirrors pkg/config.GetLeleDir without importing config
// (config depends on tui/i18n; locales must stay a leaf).
func DefaultLeleDir() string {
	if envDir := os.Getenv("LELE_CONFIG_DIR"); envDir != "" {
		if strings.HasPrefix(envDir, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				return filepath.Join(home, strings.TrimPrefix(envDir, "~"))
			}
		}
		return envDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".lele"
	}
	return filepath.Join(home, ".lele")
}

// NewManager creates a Manager with defaults (GitHub main branch, ~/.lele/locales).
func NewManager() *Manager {
	return NewManagerWithCacheDir(JoinCacheDir(DefaultLeleDir()))
}

// NewManagerWithCacheDir creates a Manager writing under cacheDir.
func NewManagerWithCacheDir(cacheDir string) *Manager {
	return &Manager{
		repo:     DefaultRepo,
		branch:   DefaultBranch,
		client:   &http.Client{Timeout: 30 * time.Second},
		cacheDir: cacheDir,
		catalog:  defaultBuiltinCatalog(),
	}
}

// SetHTTPClient overrides the HTTP client (tests).
func (m *Manager) SetHTTPClient(c *http.Client) {
	if c != nil {
		m.client = c
	}
}

// SetSource overrides repo/branch (tests or mirrors).
func (m *Manager) SetSource(repo, branch string) {
	if repo != "" {
		m.repo = repo
	}
	if branch != "" {
		m.branch = branch
	}
}

// CacheDir returns the on-disk cache root.
func (m *Manager) CacheDir() string { return m.cacheDir }

// JoinCacheDir returns <leleDir>/locales.
func JoinCacheDir(leleDir string) string {
	return filepath.Join(leleDir, "locales")
}

// rawURL builds a raw.githubusercontent.com URL for a path under locales/.
func (m *Manager) rawURL(rel string) string {
	rel = strings.TrimPrefix(rel, "/")
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/locales/%s", m.repo, m.branch, rel)
}

// TUIPath returns the cached TUI pack path for code.
func (m *Manager) TUIPath(code string) string {
	return filepath.Join(m.cacheDir, "tui", NormalizeCode(code)+".json")
}

// WebPath returns the cached WebUI pack path for code.
func (m *Manager) WebPath(code string) string {
	return filepath.Join(m.cacheDir, "web", NormalizeCode(code)+".json")
}

func (m *Manager) catalogPath() string {
	return filepath.Join(m.cacheDir, CatalogFile)
}

// IsInstalled reports whether a non-builtin pack is fully cached (TUI + Web).
func (m *Manager) IsInstalled(code string) bool {
	code = NormalizeCode(code)
	if isBuiltin(code) {
		return true
	}
	_, errT := os.Stat(m.TUIPath(code))
	_, errW := os.Stat(m.WebPath(code))
	return errT == nil && errW == nil
}

// InstalledCodes returns non-builtin languages present on disk.
func (m *Manager) InstalledCodes() []string {
	dir := filepath.Join(m.cacheDir, "tui")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		code := strings.TrimSuffix(e.Name(), ".json")
		if isBuiltin(code) {
			continue
		}
		if m.IsInstalled(code) {
			out = append(out, code)
		}
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// LoadCachedCatalog reads the on-disk catalog if present.
func (m *Manager) LoadCachedCatalog() (*Catalog, error) {
	return loadCatalogFile(m.catalogPath())
}

// RefreshCatalog fetches locales/index.json from GitHub and caches it.
func (m *Manager) RefreshCatalog(ctx context.Context) (*Catalog, error) {
	data, err := m.fetch(ctx, m.rawURL(CatalogFile))
	if err != nil {
		// Fall back to cache, then builtins.
		if c, cerr := m.LoadCachedCatalog(); cerr == nil {
			m.mu.Lock()
			m.catalog = c
			m.catalogT = time.Now()
			m.mu.Unlock()
			return c, nil
		}
		return m.snapshotCatalog(), err
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse remote catalog: %w", err)
	}
	if len(c.Languages) == 0 {
		return nil, fmt.Errorf("remote catalog has no languages")
	}
	if err := saveCatalogFile(m.catalogPath(), &c); err != nil {
		// Non-fatal: still usable in memory.
		_ = err
	}
	m.mu.Lock()
	m.catalog = &c
	m.catalogT = time.Now()
	m.mu.Unlock()
	return &c, nil
}

// Catalog returns the current catalog (memory, else disk, else builtins).
func (m *Manager) Catalog() *Catalog {
	m.mu.RLock()
	c := m.catalog
	m.mu.RUnlock()
	if c != nil && len(c.Languages) > 0 {
		return c
	}
	if cached, err := m.LoadCachedCatalog(); err == nil && len(cached.Languages) > 0 {
		m.mu.Lock()
		m.catalog = cached
		m.mu.Unlock()
		return cached
	}
	return defaultBuiltinCatalog()
}

func (m *Manager) snapshotCatalog() *Catalog {
	c := m.Catalog()
	// Shallow copy of languages slice.
	out := *c
	out.Languages = append([]Language(nil), c.Languages...)
	return &out
}

// EnsureCatalog returns a usable catalog, refreshing from GitHub when stale
// or empty. Network failure is non-fatal: builtins/cached catalog still work.
func (m *Manager) EnsureCatalog(ctx context.Context) *Catalog {
	m.mu.RLock()
	age := time.Since(m.catalogT)
	haveRemote := m.catalog != nil && m.catalogT.IsZero() == false && len(m.catalog.Languages) > 3
	m.mu.RUnlock()

	if !haveRemote || age > MaxCatalogAgeSeconds*time.Second {
		if _, err := m.RefreshCatalog(ctx); err == nil {
			return m.Catalog()
		}
	}
	return m.Catalog()
}

// List returns catalog languages with install flags.
func (m *Manager) List(ctx context.Context) []Status {
	cat := m.EnsureCatalog(ctx)
	langs := append([]Language(nil), cat.Languages...)

	// Merge builtins that the remote catalog might have omitted.
	have := map[string]bool{}
	for _, l := range langs {
		have[NormalizeCode(l.Code)] = true
	}
	for _, b := range defaultBuiltinCatalog().Languages {
		if !have[b.Code] {
			langs = append(langs, b)
		}
	}

	// Merge installed extras not listed (e.g. local drop-in).
	for _, code := range m.InstalledCodes() {
		if !have[code] {
			langs = append(langs, Language{
				Code:       code,
				Name:       code,
				NativeName: code,
			})
			have[code] = true
		}
	}

	SortLanguages(langs)
	out := make([]Status, len(langs))
	for i, l := range langs {
		l.Code = NormalizeCode(l.Code)
		out[i] = Status{
			Language:  l,
			Installed: m.IsInstalled(l.Code),
		}
	}
	return out
}

// GetTUI returns TUI translations for code: builtin codes read from a
// caller-provided embedded map; extras load from disk cache.
// embedded, when non-nil, maps code → flat translation map for builtins.
func (m *Manager) GetTUI(code string, embedded map[string]map[string]string) (map[string]string, error) {
	code = NormalizeCode(code)
	if embedded != nil {
		if t, ok := embedded[code]; ok && t != nil {
			return t, nil
		}
	}
	if isBuiltin(code) {
		return nil, fmt.Errorf("builtin language %q not provided by caller", code)
	}
	return m.readTUICache(code)
}

func (m *Manager) readTUICache(code string) (map[string]string, error) {
	data, err := os.ReadFile(m.TUIPath(code))
	if err != nil {
		return nil, fmt.Errorf("TUI pack %s not installed: %w", code, err)
	}
	var t map[string]string
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse TUI pack %s: %w", code, err)
	}
	return t, nil
}

// GetWeb returns the nested WebUI resource for code from the disk cache.
func (m *Manager) GetWeb(code string) (json.RawMessage, error) {
	code = NormalizeCode(code)
	data, err := os.ReadFile(m.WebPath(code))
	if err != nil {
		return nil, fmt.Errorf("WebUI pack %s not installed: %w", code, err)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("WebUI pack %s is not valid JSON", code)
	}
	return json.RawMessage(data), nil
}

// Install downloads TUI + Web packs for code into the cache.
// Builtin languages are a no-op success.
func (m *Manager) Install(ctx context.Context, code string) error {
	code = NormalizeCode(code)
	if code == "" {
		return fmt.Errorf("empty language code")
	}
	if isBuiltin(code) {
		return nil
	}

	tuiData, err := m.fetch(ctx, m.rawURL("tui/"+code+".json"))
	if err != nil {
		return fmt.Errorf("download TUI pack %s: %w", code, err)
	}
	webData, err := m.fetch(ctx, m.rawURL("web/"+code+".json"))
	if err != nil {
		return fmt.Errorf("download WebUI pack %s: %w", code, err)
	}

	var tuiMap map[string]string
	if err := json.Unmarshal(tuiData, &tuiMap); err != nil {
		return fmt.Errorf("invalid TUI pack %s: %w", code, err)
	}
	if len(tuiMap) == 0 {
		return fmt.Errorf("TUI pack %s is empty", code)
	}
	if !json.Valid(webData) {
		return fmt.Errorf("invalid WebUI pack %s", code)
	}

	if err := writeAtomic(m.TUIPath(code), tuiData); err != nil {
		return err
	}
	if err := writeAtomic(m.WebPath(code), webData); err != nil {
		return err
	}
	return nil
}

// Uninstall removes a cached pack. Builtins cannot be removed.
func (m *Manager) Uninstall(code string) error {
	code = NormalizeCode(code)
	if isBuiltin(code) {
		return fmt.Errorf("cannot remove builtin language %q", code)
	}
	_ = os.Remove(m.TUIPath(code))
	_ = os.Remove(m.WebPath(code))
	return nil
}

func (m *Manager) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("language pack not found on GitHub (404): %s", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}

	limited := io.LimitReader(resp.Body, MaxPackBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxPackBytes {
		return nil, fmt.Errorf("language pack exceeds %d bytes", MaxPackBytes)
	}
	return data, nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".lele-locale-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
