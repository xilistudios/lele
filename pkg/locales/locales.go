// Package locales manages downloadable language packs for lele.
//
// The binary embeds only the three core locales (es, en, pt). Additional
// languages live in the GitHub repository under locales/ and are downloaded
// on demand into ~/.lele/locales/ so the binary size stays constant.
//
// Source layout (repo):
//
//	locales/index.json       catalog of available languages
//	locales/tui/<code>.json  TUI strings (flat key→string map)
//	locales/web/<code>.json  WebUI strings (nested i18next resource)
//
// Cache layout (disk):
//
//	~/.lele/locales/index.json
//	~/.lele/locales/tui/<code>.json
//	~/.lele/locales/web/<code>.json
package locales

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// DefaultRepo is the GitHub repository that hosts language packs.
	DefaultRepo = "xilistudios/lele"
	// DefaultBranch is the branch used for raw content downloads.
	DefaultBranch = "main"
	// DefaultUserAgent identifies lele when fetching packs.
	DefaultUserAgent = "lele-locales"
	// CatalogFile is the catalog file name under the locales root.
	CatalogFile = "index.json"
	// MaxCatalogAgeSeconds is how long a cached catalog is considered fresh.
	// After this, List may refresh from GitHub when online.
	MaxCatalogAgeSeconds = 24 * 3600
	// MaxPackBytes limits a single language pack download (2 MiB).
	MaxPackBytes = 2 << 20
)

// Builtins are the locales compiled into the binary. They never need download.
var Builtins = []string{"es", "en", "pt"}

// Language describes one entry in the catalog.
type Language struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	NativeName string `json:"native_name"`
	Builtin    bool   `json:"builtin"`
	RTL        bool   `json:"rtl,omitempty"`
}

// Catalog is the root document of locales/index.json.
type Catalog struct {
	Version   int        `json:"version"`
	UpdatedAt string     `json:"updated_at,omitempty"`
	Languages []Language `json:"languages"`
}

// Status is the per-language install state returned by List.
type Status struct {
	Language
	Installed bool `json:"installed"`
}

// defaultBuiltinCatalog is used offline before the remote catalog loads.
func defaultBuiltinCatalog() *Catalog {
	return &Catalog{
		Version: 1,
		Languages: []Language{
			{Code: "es", Name: "Spanish", NativeName: "Español", Builtin: true},
			{Code: "en", Name: "English", NativeName: "English", Builtin: true},
			{Code: "pt", Name: "Portuguese", NativeName: "Português", Builtin: true},
		},
	}
}

func isBuiltin(code string) bool {
	for _, b := range Builtins {
		if b == code {
			return true
		}
	}
	return false
}

// NormalizeCode lowercases and trims a language code, mapping common aliases.
func NormalizeCode(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	// Strip region suffixes we do not ship separately (pt-BR → pt for builtins).
	switch c {
	case "español", "spanish", "es-es", "es-419":
		return "es"
	case "english", "en-us", "en-gb":
		return "en"
	case "português", "portugues", "portuguese", "pt-br", "pt-pt":
		return "pt"
	case "français", "francais", "french", "fr-fr":
		return "fr"
	case "deutsch", "german", "de-de":
		return "de"
	// Native-name aliases use unicode escapes so gosmopolitan stays quiet.
	case "\u65e5\u672c\u8a9e", "japanese", "ja-jp":
		return "ja"
	case "\u4e2d\u6587", "chinese", "zh-cn", "zh-hans":
		return "zh"
	case "\u0440\u0443\u0441\u0441\u043a\u0438\u0439", "russian", "ru-ru":
		return "ru"
	case "italiano", "italian", "it-it":
		return "it"
	case "\ud55c\uad6d\uc5b4", "korean", "ko-kr":
		return "ko"
	case "tiếng việt", "vietnamese", "vi-vn":
		return "vi"
	}
	if i := strings.IndexAny(c, "-_"); i > 0 {
		c = c[:i]
	}
	return c
}

// DisplayLabel returns "NativeName (code)" for UI pickers.
func DisplayLabel(l Language) string {
	name := l.NativeName
	if name == "" {
		name = l.Name
	}
	if name == "" {
		name = l.Code
	}
	return fmt.Sprintf("%s (%s)", name, l.Code)
}

// SortLanguages orders: builtins first (stable es/en/pt), then extras by native name.
func SortLanguages(langs []Language) {
	builtinOrder := map[string]int{"es": 0, "en": 1, "pt": 2}
	sort.SliceStable(langs, func(i, j int) bool {
		bi, bj := isBuiltin(langs[i].Code), isBuiltin(langs[j].Code)
		if bi != bj {
			return bi
		}
		if bi {
			return builtinOrder[langs[i].Code] < builtinOrder[langs[j].Code]
		}
		return strings.ToLower(langs[i].NativeName) < strings.ToLower(langs[j].NativeName)
	})
}

func loadCatalogFile(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	return &c, nil
}

func saveCatalogFile(path string, c *Catalog) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func catalogAge(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return info.ModTime().Unix()
}
