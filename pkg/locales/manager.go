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

type Manager struct {
	repo     string
	branch   string
	client   *http.Client
	cacheDir string

	mu       sync.RWMutex
	catalog  *Catalog
	catalogT time.Time
}

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

func NewManager() *Manager {
	return NewManagerWithCacheDir(JoinCacheDir(DefaultLeleDir()))
}

func NewManagerWithCacheDir(cacheDir string) *Manager {
	return &Manager{
		repo:     DefaultRepo,
		branch:   DefaultBranch,
		client:   &http.Client{Timeout: 30 * time.Second},
		cacheDir: cacheDir,
		catalog:  defaultBuiltinCatalog(),
	}
}

func (m *Manager) SetHTTPClient(c *http.Client) {
	if c != nil {
		m.client = c
	}
}

func (m *Manager) SetSource(repo, branch string) {
	if repo != "" {
		m.repo = repo
	}
	if branch != "" {
		m.branch = branch
	}
}

func (m *Manager) CacheDir() string { return m.cacheDir }

func JoinCacheDir(leleDir string) string {
	return filepath.Join(leleDir, "locales")
}

func (m *Manager) rawURL(rel string) string {
	rel = strings.TrimPrefix(rel, "/")
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/locales/%s", m.repo, m.branch, rel)
}

func (m *Manager) TUIPath(code string) string {
	return filepath.Join(m.cacheDir, "tui", NormalizeCode(code)+".json")
}

func (m *Manager) WebPath(code string) string {
	return filepath.Join(m.cacheDir, "web", NormalizeCode(code)+".json")
}

func (m *Manager) catalogPath() string {
	return filepath.Join(m.cacheDir, CatalogFile)
}

func (m *Manager) IsInstalled(code string) bool {
	code = NormalizeCode(code)
	if isBuiltin(code) {
		return true
	}
	_, errT := os.Stat(m.TUIPath(code))
	_, errW := os.Stat(m.WebPath(code))
	return errT == nil && errW == nil
}

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

func (m *Manager) LoadCachedCatalog() (*Catalog, error) {
	return loadCatalogFile(m.catalogPath())
}

func (m *Manager) RefreshCatalog(ctx context.Context) (*Catalog, error) {
	data, err := m.fetch(ctx, m.rawURL(CatalogFile))
	if err != nil {
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
		_ = err
	}
	m.mu.Lock()
	m.catalog = &c
	m.catalogT = time.Now()
	m.mu.Unlock()
	return &c, nil
}

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
	out := *c
	out.Languages = append([]Language(nil), c.Languages...)
	return &out
}

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

func (m *Manager) List(ctx context.Context) []Status {
	cat := m.EnsureCatalog(ctx)
	langs := append([]Language(nil), cat.Languages...)

	have := map[string]bool{}
	for _, l := range langs {
		have[NormalizeCode(l.Code)] = true
	}
	for _, b := range defaultBuiltinCatalog().Languages {
		if !have[b.Code] {
			langs = append(langs, b)
		}
	}

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
