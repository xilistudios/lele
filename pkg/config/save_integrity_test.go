package config

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestSaveConfigRebaselinesToWrittenSnapshot(t *testing.T) {
	path := writeSaveFixture(t, `{"channels":{"telegram":{"verbose":"off"}}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.TelegramVerbose(); got != VerboseOff {
		t.Fatalf("precondition: verbose = %q, want off", got)
	}

	if err := saveConfig(path, cfg, func() { cfg.SetTelegramVerbose(VerboseFull) }); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig (settling): %v", err)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if got := reloaded.TelegramVerbose(); got != VerboseFull {
		t.Fatalf("on-disk verbose = %q after a settling save, want %q: the mutation acknowledged between the snapshot and the write was never persisted", got, VerboseFull)
	}
}

func TestSaveConfigOverCorruptFileWritesNothing(t *testing.T) {
	t.Setenv("LELE_TEST_CORRUPT_KEY", "sk-resolved-secret")

	path := writeSaveFixture(t, `{
  "providers": {"groq": {"api_key": "{{ENV_LELE_TEST_CORRUPT_KEY}}"}},
  "future_section": {"keep": true},
  "gateway": {"port": 8080}
}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.Gateway.Port = 9090

	const corrupt = `{"gateway": {"port": 9090`
	if err := os.WriteFile(path, []byte(corrupt), 0600); err != nil {
		t.Fatalf("write corrupt config: %v", err)
	}

	if err := SaveConfig(path, cfg); err == nil {
		t.Fatal("SaveConfig over an unparsable config succeeded; want an error and nothing written")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != corrupt {
		t.Fatalf("unparsable config was rewritten:\n%s", after)
	}
}

func TestSaveConfigOverMissingFileKeepsRawDocument(t *testing.T) {
	t.Setenv("LELE_TEST_MISSING_KEY", "sk-resolved-secret")

	path := writeSaveFixture(t, `{
  "providers": {"groq": {"api_key": "{{ENV_LELE_TEST_MISSING_KEY}}"}},
  "future_section": {"keep": true},
  "gateway": {"port": 8080}
}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.Gateway.Port = 9090

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig over a missing file: %v", err)
	}

	saved := readSavedString(t, path)
	if strings.Contains(saved, "sk-resolved-secret") {
		t.Errorf("resolved env secret was inlined into the file:\n%s", saved)
	}
	if !strings.Contains(saved, "{{ENV_LELE_TEST_MISSING_KEY}}") {
		t.Errorf("env placeholder was lost:\n%s", saved)
	}
	if !strings.Contains(saved, `"future_section"`) {
		t.Errorf("unmodelled section was dropped:\n%s", saved)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if reloaded.Gateway.Port != 9090 {
		t.Errorf("reloaded gateway.port = %d, want the saved 9090", reloaded.Gateway.Port)
	}
}

func TestSaveConfigPersistsFalseAgainstATrueDefault(t *testing.T) {
	path := writeSaveFixture(t, `{"gateway":{"port":8080}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Session.EvictExcludedFromMemory {
		t.Fatal("precondition: evict_excluded_from_memory defaults to true")
	}

	cfg.Session.EvictExcludedFromMemory = false
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	saved := readSavedString(t, path)
	if !strings.Contains(saved, `"evict_excluded_from_memory": false`) {
		t.Fatalf("saved file does not pin the off value:\n%s", saved)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if reloaded.Session.EvictExcludedFromMemory {
		t.Error("evict_excluded_from_memory reloaded as true, want the saved false")
	}
}

func TestDefaultTrueBoolsAreNotOmitted(t *testing.T) {
	defaults := reflect.ValueOf(*DefaultConfig())
	for i := 0; i < defaults.NumField(); i++ {
		walkDefaultTrueBools(t, defaults.Field(i), defaults.Type().Field(i).Name+".")
	}
}

func walkDefaultTrueBools(t *testing.T, value reflect.Value, prefix string) {
	t.Helper()
	switch value.Kind() {
	case reflect.Ptr:
		if !value.IsNil() {
			walkDefaultTrueBools(t, value.Elem(), prefix)
		}
	case reflect.Struct:
		typ := value.Type()
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}
			tag := field.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name == "" {
				name = field.Name
			}
			if value.Field(i).Kind() == reflect.Bool && value.Field(i).Bool() && strings.Contains(tag, ",omitempty") {
				t.Errorf("%s%s is true by default with json `%s`: saving false drops the key and reloads as the default true", prefix, name, tag)
			}
			walkDefaultTrueBools(t, value.Field(i), prefix+name+".")
		}
	}
}

func TestSaveConfigPrunesSectionEmptiedByDeletion(t *testing.T) {
	path := writeSaveFixture(t, `{"gateway":{"port":8080},"session":{"compaction_model":"openrouter:gemini"}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.Session.CompactionModel = ""
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	parsed := readSavedJSON(t, path)
	if _, ok := parsed["session"]; ok {
		t.Fatalf("session was emptied by the deletion and must be pruned, got %v", parsed["session"])
	}
	if keys := sortedKeys(t, parsed); !reflect.DeepEqual(keys, []string{"gateway"}) {
		t.Fatalf("saved top-level keys = %v, want exactly [gateway]", keys)
	}
}

func TestLoadConfigNamesMisplacedEnvPlaceholder(t *testing.T) {
	t.Setenv("LELE_TEST_BARE_PORT", "8080")

	cases := map[string]string{
		"object value": `{"gateway":{"port":{{ENV_LELE_TEST_BARE_PORT}}}}`,
		"array value":  `{"channels":{"telegram":{"allow_from":[{{ENV_LELE_TEST_BARE_PORT}}]}}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeSaveFixture(t, content)
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatal("LoadConfig over a placeholder outside a JSON string succeeded; want an error")
			}
			message := err.Error()
			if !strings.Contains(message, "{{ENV_LELE_TEST_BARE_PORT}}") {
				t.Errorf("error does not name the placeholder: %v", err)
			}
			expected := "byte offset " + strconv.Itoa(len(strings.Split(content, "{{ENV_")[0]))
			if !strings.Contains(message, expected) {
				t.Errorf("error does not report the position %q: %v", expected, err)
			}
			if !strings.Contains(message, "not inside a JSON string") {
				t.Errorf("error does not state the rule: %v", err)
			}
		})
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
func TestApplyChangesDoesNotCreateEmptiedSections(t *testing.T) {
	raw := map[string]json.RawMessage{
		"gateway": json.RawMessage(`{"port": 8080}`),
		"session": json.RawMessage(`{"compaction_model": "openrouter:gemini"}`),
	}
	changes := []change{
		{path: []string{"session", "compaction_model"}, remove: true},
		{path: []string{"absent_section", "future_key"}, remove: true},
	}

	out, err := applyChanges(raw, changes)
	if err != nil {
		t.Fatalf("applyChanges: %v", err)
	}
	if _, ok := out["session"]; ok {
		t.Errorf("section emptied by a deletion was left behind: %s", out["session"])
	}
	if _, ok := out["absent_section"]; ok {
		t.Error("a deletion of a key in an absent section must not create the section")
	}
	if string(out["gateway"]) != string(raw["gateway"]) {
		t.Errorf("untouched section changed: %s", out["gateway"])
	}
}
