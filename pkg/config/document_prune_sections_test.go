package config

import (
	"reflect"
	"testing"
)

// --- pruning: tools ----------------------------------------------------------

// TestPrune_ToolsCronTimeoutWritesOnlyThatKey pins the goal of the tools
// prune: one customized nested value must not resurrect the sibling defaults
// (exec, brave, duckduckgo, perplexity and searxng used to be written with
// every field on every save).
func TestPrune_ToolsCronTimeoutWritesOnlyThatKey(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Tools.Cron.ExecTimeoutMinutes = 42
	path := saveDoc(t, t.TempDir(), doc)

	parsed := readSavedJSON(t, path)
	tools := savedSection(t, parsed, "tools")
	if got := keysOf(tools); len(got) != 1 || got[0] != "cron" {
		t.Fatalf("tools keys = %v, want exactly [cron]", got)
	}
	cron := savedBlock(t, tools, "cron", "exec_timeout_minutes")
	if cron["exec_timeout_minutes"] != float64(42) {
		t.Errorf("cron.exec_timeout_minutes = %v, want 42", cron["exec_timeout_minutes"])
	}
	// The file is agents + that one value, nothing else.
	if got := keysOf(parsed); len(got) != 2 {
		t.Errorf("saved top-level keys = %v, want exactly [agents tools]", got)
	}

	// The untouched siblings reload with their code defaults.
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	def := DefaultConfig()
	if loaded.Tools.Cron.ExecTimeoutMinutes != 42 {
		t.Errorf("reloaded cron timeout = %d, want 42", loaded.Tools.Cron.ExecTimeoutMinutes)
	}
	if !reflect.DeepEqual(loaded.Tools.Web, def.Tools.Web) {
		t.Errorf("reloaded tools.web mismatch:\n got=%#v\nwant=%#v", loaded.Tools.Web, def.Tools.Web)
	}
	if !reflect.DeepEqual(loaded.Tools.Exec, def.Tools.Exec) {
		t.Errorf("reloaded tools.exec mismatch:\n got=%#v\nwant=%#v", loaded.Tools.Exec, def.Tools.Exec)
	}
}

// TestPrune_ToolsWebSearchKeyringPlaceholderStillSerialized pins that a
// placeholder secret is never pruned away, even though every plain field of
// the engine still equals its default.
func TestPrune_ToolsWebSearchKeyringPlaceholderStillSerialized(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Tools.Web.Perplexity.Enabled = true
	doc.Tools.Web.Perplexity.APIKey = SecretValue{Mode: SecretModeKeyring, SecretName: "perplexity.api_key"}
	path := saveDoc(t, t.TempDir(), doc)

	tools := savedSection(t, readSavedJSON(t, path), "tools")
	web := savedSection(t, tools, "web")
	perplexity := savedBlock(t, web, "perplexity", "enabled", "api_key")
	if perplexity["api_key"] != "{{SECRET:perplexity.api_key}}" {
		t.Errorf("perplexity.api_key = %v, want the keyring placeholder", perplexity["api_key"])
	}
	if got := keysOf(web); len(got) != 1 || got[0] != "perplexity" {
		t.Errorf("tools.web keys = %v, want exactly [perplexity]", got)
	}
	if got := keysOf(tools); len(got) != 1 || got[0] != "web" {
		t.Errorf("tools keys = %v, want exactly [web]", got)
	}

	// The document keeps the placeholder (not a resolved value) on reload.
	loaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}
	if got := loaded.Tools.Web.Perplexity.APIKey; got.Mode != SecretModeKeyring || got.SecretName != "perplexity.api_key" {
		t.Errorf("reloaded perplexity api key = %#v, want keyring mode for perplexity.api_key", got)
	}
	if !loaded.Tools.Web.Perplexity.Enabled {
		t.Error("reloaded perplexity.enabled = false, want true")
	}
	// An engine that was never customized keeps its defaults through the
	// overlay even though the pruned file says nothing about it.
	if ddg := loaded.Tools.Web.DuckDuckGo; !ddg.Enabled || ddg.MaxResults != 5 {
		t.Errorf("reloaded duckduckgo = %#v, want the code default {true 5}", ddg)
	}
}

// TestPrune_ToolsEngineWithOnlyPlaceholderWritesOnlyAPIKey covers the disabled
// engine case: a key alone counts as configured and must not drag the
// default-valued "enabled: false" into the file.
func TestPrune_ToolsEngineWithOnlyPlaceholderWritesOnlyAPIKey(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Tools.Web.Brave.APIKey = SecretValue{Mode: SecretModeEnv, EnvName: "LELE_BRAVE_KEY"}
	path := saveDoc(t, t.TempDir(), doc)

	tools := savedSection(t, readSavedJSON(t, path), "tools")
	web := savedSection(t, tools, "web")
	brave := savedBlock(t, web, "brave", "api_key")
	if brave["api_key"] != "{{ENV_LELE_BRAVE_KEY}}" {
		t.Errorf("brave.api_key = %v, want the env placeholder", brave["api_key"])
	}
}

// TestPrune_ExecTimeoutSecondsZeroIsARealDifference guards the "0 means no
// timeout" contract of tools.exec.timeout_seconds: 0 differs from the 60s
// default, so it must keep being written.
func TestPrune_ExecTimeoutSecondsZeroIsARealDifference(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Tools.Exec.TimeoutSeconds = 0
	path := saveDoc(t, t.TempDir(), doc)

	tools := savedSection(t, readSavedJSON(t, path), "tools")
	exec := savedBlock(t, tools, "exec", "timeout_seconds")
	if exec["timeout_seconds"] != float64(0) {
		t.Errorf("exec.timeout_seconds = %v, want 0 (explicit no-timeout)", exec["timeout_seconds"])
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Tools.Exec.TimeoutSeconds != 0 {
		t.Errorf("reloaded timeout_seconds = %d, want 0", loaded.Tools.Exec.TimeoutSeconds)
	}
}

// TestPrune_ExecWhitelistWritesOnlyItsOwnKey checks a slice key is pruned
// independently of the flags next to it.
func TestPrune_ExecWhitelistWritesOnlyItsOwnKey(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Tools.Exec.WhitelistCommands = []string{"git status", "ls"}
	path := saveDoc(t, t.TempDir(), doc)

	tools := savedSection(t, readSavedJSON(t, path), "tools")
	savedBlock(t, tools, "exec", "whitelist_commands")
	if got := keysOf(tools); len(got) != 1 || got[0] != "exec" {
		t.Errorf("tools keys = %v, want exactly [exec]", got)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !reflect.DeepEqual(loaded.Tools.Exec.WhitelistCommands, []string{"git status", "ls"}) {
		t.Errorf("reloaded whitelist = %v", loaded.Tools.Exec.WhitelistCommands)
	}
	if !loaded.Tools.Exec.EnableDenyPatterns {
		t.Error("reloaded enable_deny_patterns = false, want the default true")
	}
}

// --- pruning: heartbeat / devices / logs -------------------------------------

// TestPrune_ClearingADefaultedListStillWritesEmptyArray covers the one place
// where a pruned list has a non-empty code default (native.cors_origins). The
// clear must be written as [], because JSON null makes encoding/json skip the
// field and resurrect the defaults.
func TestPrune_ClearingADefaultedListStillWritesEmptyArray(t *testing.T) {
	doc := defaultEditableDocument()
	if len(doc.Channels.Native.CORSOrigins) == 0 {
		t.Fatal("test expects native.cors_origins to have a non-empty default")
	}
	doc.Channels.Native.CORSOrigins = nil

	path := saveDoc(t, t.TempDir(), doc)
	parsed := readSavedJSON(t, path)
	channels := savedSection(t, parsed, "channels")
	native, ok := channels["native"].(map[string]interface{})
	if !ok {
		t.Fatalf("saved JSON has no channels.native object: %v", channels)
	}
	raw, ok := native["cors_origins"]
	if !ok {
		t.Fatalf("cleared cors_origins must still be written, native block = %v", native)
	}
	list, ok := raw.([]interface{})
	if !ok || len(list) != 0 {
		t.Fatalf("cleared cors_origins = %v (%T), want an empty array", raw, raw)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(loaded.Channels.Native.CORSOrigins) != 0 {
		t.Errorf("cleared cors_origins came back as %v, want empty", loaded.Channels.Native.CORSOrigins)
	}
}

func TestPrune_HeartbeatDevicesLogsCustomizedWriteOnlyDiffingKeys(t *testing.T) {
	doc := defaultEditableDocument()
	def := DefaultConfig()
	// Only the interval differs: heartbeat.enabled stays at its default true.
	doc.Heartbeat.Interval = 15
	// Only enabled differs: devices.monitor_usb stays at its default true.
	doc.Devices.Enabled = true
	// Only max_days and rotation differ.
	doc.Logs.MaxDays = 30
	doc.Logs.Rotation = "weekly"
	path := saveDoc(t, t.TempDir(), doc)

	parsed := readSavedJSON(t, path)
	savedBlock(t, parsed, "heartbeat", "interval")
	savedBlock(t, parsed, "devices", "enabled")
	savedBlock(t, parsed, "logs", "max_days", "rotation")

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Heartbeat.Enabled != def.Heartbeat.Enabled || loaded.Heartbeat.Interval != 15 {
		t.Errorf("reloaded heartbeat = %#v, want {true 15}", loaded.Heartbeat)
	}
	if !loaded.Devices.Enabled || !loaded.Devices.MonitorUSB {
		t.Errorf("reloaded devices = %#v, want {true true}", loaded.Devices)
	}
	if !loaded.Logs.Enabled || loaded.Logs.Path != def.Logs.Path ||
		loaded.Logs.MaxDays != 30 || loaded.Logs.Rotation != "weekly" {
		t.Errorf("reloaded logs = %#v", loaded.Logs)
	}
}

func TestPrune_DisabledHeartbeatWritesOnlyEnabledFalse(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Heartbeat.Enabled = false // interval stays at its default
	path := saveDoc(t, t.TempDir(), doc)

	heartbeat := savedBlock(t, readSavedJSON(t, path), "heartbeat", "enabled")
	if heartbeat["enabled"] != false {
		t.Errorf("heartbeat.enabled = %v, want false", heartbeat["enabled"])
	}
}

// --- pruning: session --------------------------------------------------------

func TestPrune_SessionTriStateFalseIsStillWritten(t *testing.T) {
	// An explicit false on a tri-state pointer is a user decision: the key
	// keeps being written, because nil and false are runtime-equivalent but
	// not document-equivalent (correctness beats minimalism).
	doc := defaultEditableDocument()
	yes, no := true, false
	doc.Session.DurableInbound = &yes
	doc.Session.DurableOutbound = &no
	doc.Session.Resume = &no
	path := saveDoc(t, t.TempDir(), doc)

	session := savedSection(t, readSavedJSON(t, path), "session")
	hasKey(t, session, "durable_inbound")
	hasKey(t, session, "durable_outbound")
	hasKey(t, session, "resume_enabled")
	if len(session) != 3 {
		t.Errorf("session keys = %v, want only the three tri-state keys", keysOf(session))
	}

	reloaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}
	if reloaded.Session.DurableInbound == nil || !*reloaded.Session.DurableInbound {
		t.Error("durable_inbound lost through the round-trip")
	}
	if reloaded.Session.DurableOutbound == nil || *reloaded.Session.DurableOutbound {
		t.Error("durable_outbound=false lost through the round-trip")
	}
	if reloaded.Session.Resume == nil || *reloaded.Session.Resume {
		t.Error("resume_enabled=false lost through the round-trip")
	}
}

func TestPrune_SessionEvictExcludedFromMemoryDefaultsToTrue(t *testing.T) {
	// DefaultConfig() has evict_excluded_from_memory:true and LoadConfig has
	// no override for it, so the key is pruned and still reloads as true.
	doc := defaultEditableDocument()
	if !doc.Session.EvictExcludedFromMemory {
		t.Fatal("test precondition: the document default must be true")
	}
	doc.Session.DMScope = "channel" // force a session section
	path := saveDoc(t, t.TempDir(), doc)

	session := savedSection(t, readSavedJSON(t, path), "session")
	if _, ok := session["evict_excluded_from_memory"]; ok {
		t.Errorf("evict_excluded_from_memory equals its default and must be pruned, got %v", session)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !loaded.Session.EvictExcludedFromMemory {
		t.Error("reloaded evict_excluded_from_memory = false, want the default true")
	}
}

func TestPrune_SessionThresholdsCustomizedWriteOnlyThemselves(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Session.EphemeralThreshold = 300
	path := saveDoc(t, t.TempDir(), doc)

	hasKey(t, savedSection(t, readSavedJSON(t, path), "session"), "ephemeral_threshold")

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Session.EphemeralThreshold != 300 {
		t.Errorf("reloaded ephemeral_threshold = %d, want 300", loaded.Session.EphemeralThreshold)
	}
	if loaded.Session.CompactionThresholdPercent != DefaultCompactionThresholdPercent {
		t.Errorf("reloaded compaction percent = %d, want the default %d",
			loaded.Session.CompactionThresholdPercent, DefaultCompactionThresholdPercent)
	}
}

// --- full round-trip pin for the newly pruned sections -----------------------

func TestPrune_RichSessionToolsHeartbeatDevicesLogsRoundTrip(t *testing.T) {
	yes, no := true, false
	cfg := DefaultConfig()

	cfg.Session = SessionConfig{
		DMScope:                    "channel",
		IdentityLinks:              map[string][]string{"agent:main": {"telegram:1", "discord:2"}},
		Ephemeral:                  true,
		EphemeralThreshold:         300,
		CompactionThresholdPercent: 50,
		CompactionModel:            "openrouter:gemini",
		EvictExcludedFromMemory:    false,
		DurableInbound:             &yes,
		DurableOutbound:            &no,
		Resume:                     &yes,
	}
	cfg.Tools = ToolsConfig{
		Web: WebToolsConfig{
			Brave:      BraveConfig{Enabled: true, APIKey: "brave-literal", MaxResults: 10},
			DuckDuckGo: DuckDuckGoConfig{Enabled: false, MaxResults: 3},
			Perplexity: PerplexityConfig{Enabled: true, APIKey: "pplx-literal", MaxResults: 7},
			SearXNG: SearXNGConfig{
				Enabled: true, InstanceURL: "https://searx.local", Categories: "news,it",
				Language: "es", SafeSearch: 2, MaxResults: 9,
			},
		},
		Cron: CronToolsConfig{ExecTimeoutMinutes: 42},
		Exec: ExecConfig{
			EnableDenyPatterns: false, CustomDenyPatterns: []string{"rm -rf", "shutdown"},
			TimeoutSeconds: 0, WhitelistCommands: []string{"git status"},
		},
	}
	cfg.Heartbeat = HeartbeatConfig{Enabled: false, Interval: 15}
	cfg.Devices = DevicesConfig{Enabled: true, MonitorUSB: false}
	cfg.Logs = LogsConfig{Enabled: true, Path: "/var/log/lele", MaxDays: 30, Rotation: "weekly"}

	path := saveDoc(t, t.TempDir(), editableDocumentFromConfig(cfg))

	loaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}
	got, err := loaded.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig failed: %v", err)
	}
	for _, tc := range []struct {
		name      string
		got, want interface{}
	}{
		{"session", got.Session, cfg.Session},
		{"tools", got.Tools, cfg.Tools},
		{"heartbeat", got.Heartbeat, cfg.Heartbeat},
		{"devices", got.Devices, cfg.Devices},
		{"logs", got.Logs, cfg.Logs},
	} {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s round-trip mismatch:\n got=%#v\nwant=%#v", tc.name, tc.got, tc.want)
		}
	}

	// The same values must come back through the plain runtime loader, which is
	// what the gateway and the config watcher use.
	runtime, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !reflect.DeepEqual(runtime.Session, cfg.Session) {
		t.Errorf("LoadConfig session mismatch:\n got=%#v\nwant=%#v", runtime.Session, cfg.Session)
	}
	if !reflect.DeepEqual(runtime.Tools, cfg.Tools) {
		t.Errorf("LoadConfig tools mismatch:\n got=%#v\nwant=%#v", runtime.Tools, cfg.Tools)
	}
	if !reflect.DeepEqual(runtime.Heartbeat, cfg.Heartbeat) || !reflect.DeepEqual(runtime.Devices, cfg.Devices) ||
		!reflect.DeepEqual(runtime.Logs, cfg.Logs) {
		t.Errorf("LoadConfig heartbeat/devices/logs mismatch: %#v %#v %#v", runtime.Heartbeat, runtime.Devices, runtime.Logs)
	}
}

// TestPrune_OverlayKeepsDefaultsForPrunedKeys is the regression guard for the
// bug the tools prune exposed: the placeholder overlay used to replace a whole
// section with a zero-valued struct whenever the file mentioned it, so every
// default-valued key pruned by toSerializable came back as its zero value
// (duckduckgo disabled, searxng without categories/language, telegram without
// a verbose level, the LINE webhook emptied...).
func TestPrune_OverlayKeepsDefaultsForPrunedKeys(t *testing.T) {
	def := DefaultConfig()

	doc := defaultEditableDocument()
	// One key per section, everything else left at its default.
	doc.Tools.Web.Brave.APIKey = SecretValue{Mode: SecretModeLiteral, Value: "brave-key"}
	doc.Tools.Exec.CustomDenyPatterns = []string{"rm -rf"}
	doc.Heartbeat.Interval = 15
	doc.Logs.MaxDays = 30
	doc.Session.CompactionModel = "openrouter:gemini"
	doc.Channels.Telegram.Token = SecretValue{Mode: SecretModeKeyring, SecretName: "telegram.token"}
	doc.Channels.LINE.WebhookPort = 19090
	doc.Channels.OneBot.ReconnectInterval = 9
	path := saveDoc(t, t.TempDir(), doc)

	loaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}

	// Tools: engines the file never mentions keep their non-zero defaults.
	if ddg := loaded.Tools.Web.DuckDuckGo; !reflect.DeepEqual(ddg, def.Tools.Web.DuckDuckGo) {
		t.Errorf("duckduckgo = %#v, want the default %#v", ddg, def.Tools.Web.DuckDuckGo)
	}
	if got := loaded.Tools.Web.SearXNG; !reflect.DeepEqual(got, EditableSearXNGConfig(def.Tools.Web.SearXNG)) {
		t.Errorf("searxng = %#v, want the default %#v", got, def.Tools.Web.SearXNG)
	}
	if got := loaded.Tools.Web.Brave; got.APIKey.Mode != SecretModeLiteral || got.APIKey.Value != "brave-key" {
		t.Errorf("brave api key = %#v, want the saved literal", got.APIKey)
	}
	if got := loaded.Tools.Exec; !got.EnableDenyPatterns || got.TimeoutSeconds != 60 {
		t.Errorf("exec = %#v, want the untouched defaults", got)
	}
	// Channels: same rule for the sections that were already pruned.
	if got := loaded.Channels.Telegram; got.Verbose != def.Channels.Telegram.Verbose {
		t.Errorf("telegram.verbose = %q, want the default %q", got.Verbose, def.Channels.Telegram.Verbose)
	}
	if got := loaded.Channels.LINE; got.WebhookHost != def.Channels.LINE.WebhookHost ||
		got.WebhookPath != def.Channels.LINE.WebhookPath || got.WebhookPort != 19090 {
		t.Errorf("line = %#v, want the defaults plus the custom port", got)
	}
	if got := loaded.Channels.OneBot; got.WSUrl != def.Channels.OneBot.WSUrl || got.ReconnectInterval != 9 {
		t.Errorf("onebot = %#v, want the default ws_url plus the custom interval", got)
	}

	// And saving that document again must be a no-op on the file.
	reSaved := saveDoc(t, t.TempDir(), loaded)
	if !reflect.DeepEqual(readSavedJSON(t, path), readSavedJSON(t, reSaved)) {
		t.Errorf("re-saving the reloaded document changed the file:\n first=%v\n second=%v",
			readSavedJSON(t, path), readSavedJSON(t, reSaved))
	}
}
