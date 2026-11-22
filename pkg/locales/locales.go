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
	DefaultRepo          = "xilistudios/lele"
	DefaultBranch        = "main"
	DefaultUserAgent     = "lele-locales"
	CatalogFile          = "index.json"
	MaxCatalogAgeSeconds = 24 * 3600
	MaxPackBytes         = 2 << 20
)

var Builtins = []string{"es", "en", "pt"}

type Language struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	NativeName string `json:"native_name"`
	Builtin    bool   `json:"builtin"`
	RTL        bool   `json:"rtl,omitempty"`
}

type Catalog struct {
	Version   int        `json:"version"`
	UpdatedAt string     `json:"updated_at,omitempty"`
	Languages []Language `json:"languages"`
}

type Status struct {
	Language
	Installed bool `json:"installed"`
}

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

func NormalizeCode(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
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
