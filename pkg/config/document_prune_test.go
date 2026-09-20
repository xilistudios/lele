package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// --- prune-on-save helpers for tests ---------------------------------------

// readSavedJSON parses the raw JSON written by SaveEditableDocument so tests
// can assert on the actual file shape (absence of keys), not just round-trips.
func readSavedJSON(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal of saved file failed: %v\n%s", err, string(data))
	}
	return parsed
}

// savedSection returns the raw object stored under key at the top level of a
// saved file, failing when it is absent or not an object.
func savedSection(t *testing.T, parsed map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	raw, ok := parsed[key]
	if !ok {
		t.Fatalf("saved JSON has no %q section; keys: %v", key, keysOf(parsed))
	}
	section, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("%s is %T, want object", key, raw)
	}
	return section
}

// savedBlock returns a nested block and asserts its exact key set. It works on
// any parent object (channels, tools, tools.web).
func savedBlock(t *testing.T, parent map[string]interface{}, name string, wantKeys ...string) map[string]interface{} {
	t.Helper()
	raw, ok := parent[name]
	if !ok {
		t.Fatalf("block %q missing from saved JSON; present: %v", name, keysOf(parent))
	}
	block, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("block %q is %T, want object", name, raw)
	}
	if len(block) != len(wantKeys) {
		t.Fatalf("block %q has keys %v, want exactly %v", name, keysOf(block), wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := block[k]; !ok {
			t.Fatalf("block %q missing key %q (has %v)", name, k, keysOf(block))
		}
	}
	return block
}

// hasKey asserts that a scalar key is present in an object.
func hasKey(t *testing.T, obj map[string]interface{}, key string) interface{} {
	t.Helper()
	value, ok := obj[key]
	if !ok {
		t.Fatalf("key %q missing from saved JSON; present: %v", key, keysOf(obj))
	}
	return value
}

func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func saveDoc(t *testing.T, dir string, doc *EditableDocument) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument failed: %v", err)
	}
	return path
}

// --- pruning: a document equal to the defaults writes nothing ---------------

// prunableSections are the sections toSerializable now emits key-by-key
// instead of unconditionally.
var prunableSections = []string{"session", "tools", "heartbeat", "devices", "logs", "channels", "gateway"}

func TestPrune_DefaultsWriteOnlyAgents(t *testing.T) {
	doc := defaultEditableDocument()
	path := saveDoc(t, t.TempDir(), doc)

	parsed := readSavedJSON(t, path)

	for _, section := range prunableSections {
		if raw, ok := parsed[section]; ok {
			t.Errorf("saved JSON must not contain a %q section when everything equals the defaults, got: %v", section, raw)
		}
	}
	// The whole file is agents and nothing else: onboarding's minimal
	// config.json has no dead weight left in it.
	if got := keysOf(parsed); len(got) != 1 || got[0] != "agents" {
		t.Errorf("saved top-level keys = %v, want exactly [agents]", got)
	}
	// Sanity: the document really was serialized (other sections present).
	if _, ok := parsed["agents"]; !ok {
		t.Fatal("expected an \"agents\" section to still be written")
	}
}

// TestPrune_DefaultsRoundTripKeepsRuntimeDefaults is the safety net for the
// test above: everything the pruned sections used to spell out must still come
// back as the code default after a save + reload of the minimal file.
func TestPrune_DefaultsRoundTripKeepsRuntimeDefaults(t *testing.T) {
	def := DefaultConfig()
	path := saveDoc(t, t.TempDir(), defaultEditableDocument())

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	// The session comparison uses the file defaults: DefaultConfig() holds
	// ephemeral:true, but a file that omits session.ephemeral reloads as false
	// (LoadConfig's special handling), which is exactly what the pruned
	// document means. See TestPrune_SessionEphemeralAbsentMeansFalse.
	fileDefaults := DefaultConfig()
	fileDefaults.Session.Ephemeral = false
	if !reflect.DeepEqual(loaded.Session, fileDefaults.Session) {
		t.Errorf("session round-trip mismatch:\n got=%#v\nwant=%#v", loaded.Session, fileDefaults.Session)
	}
	if !reflect.DeepEqual(loaded.Tools, def.Tools) {
		t.Errorf("tools round-trip mismatch:\n got=%#v\nwant=%#v", loaded.Tools, def.Tools)
	}
	if !reflect.DeepEqual(loaded.Heartbeat, def.Heartbeat) {
		t.Errorf("heartbeat round-trip mismatch:\n got=%#v\nwant=%#v", loaded.Heartbeat, def.Heartbeat)
	}
	if !reflect.DeepEqual(loaded.Devices, def.Devices) {
		t.Errorf("devices round-trip mismatch:\n got=%#v\nwant=%#v", loaded.Devices, def.Devices)
	}
	if !reflect.DeepEqual(loaded.Logs, def.Logs) {
		t.Errorf("logs round-trip mismatch:\n got=%#v\nwant=%#v", loaded.Logs, def.Logs)
	}
}

// TestPrune_SessionEphemeralAbsentMeansFalse pins the one field whose prune
// value is NOT DefaultConfig(): LoadConfig forces session.ephemeral=false when
// the key is missing (DefaultConfig() says true), so false must be omitted and
// true must still be written — omitting it would silently flip the feature off.
func TestPrune_SessionEphemeralAbsentMeansFalse(t *testing.T) {
	dir := t.TempDir()

	// false (the effective file default) writes no key at all.
	doc := defaultEditableDocument()
	doc.Session.CompactionModel = "openrouter:gemini" // force a session section
	path := saveDoc(t, dir, doc)

	session := savedSection(t, readSavedJSON(t, path), "session")
	if _, ok := session["ephemeral"]; ok {
		t.Errorf("ephemeral=false is the file default and must be pruned, got session: %v", session)
	}
	if got := keysOf(session); len(got) != 1 || got[0] != "compaction_model" {
		t.Errorf("session keys = %v, want exactly [compaction_model]", got)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Session.Ephemeral {
		t.Error("LoadConfig on a file without session.ephemeral = true, want false")
	}
	// ToConfig must agree with LoadConfig on the same file.
	reloaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}
	docCfg, err := reloaded.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig failed: %v", err)
	}
	if docCfg.Session.Ephemeral {
		t.Error("ToConfig of the reloaded document = true, want false (must mirror LoadConfig)")
	}
	if reloaded.Session.Ephemeral {
		t.Error("reloaded document ephemeral = true, want false")
	}
}

func TestPrune_SessionEphemeralTrueIsARealDifference(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Session.Ephemeral = true
	path := saveDoc(t, t.TempDir(), doc)

	parsed := readSavedJSON(t, path)
	session := savedSection(t, parsed, "session")
	if got := keysOf(session); len(got) != 1 || got[0] != "ephemeral" {
		t.Fatalf("session keys = %v, want exactly [ephemeral] (nothing else differs)", got)
	}
	if session["ephemeral"] != true {
		t.Errorf("session.ephemeral = %v, want true", session["ephemeral"])
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !loaded.Session.Ephemeral {
		t.Error("an explicit ephemeral:true must survive the reload, got false")
	}
}

func TestPrune_DefaultNativeAndWebWriteNothing(t *testing.T) {
	// Default native is enabled:true and default web is enabled:true:
	// neither may appear in a saved default document.
	doc := defaultEditableDocument()
	// Touch one unrelated channel so the channels section exists at all.
	doc.Channels.Discord.Enabled = true
	path := saveDoc(t, t.TempDir(), doc)

	channels := savedSection(t, readSavedJSON(t, path), "channels")
	if _, ok := channels["native"]; ok {
		t.Errorf("default native must write nothing, got %v", channels["native"])
	}
	if _, ok := channels["web"]; ok {
		t.Errorf("default web must write nothing, got %v", channels["web"])
	}
	savedBlock(t, channels, "discord", "enabled")
}

// --- pruning: disabled native / web ------------------------------------------

func TestPrune_DisabledNativeWritesOnlyEnabledFalse(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Channels.Native.Enabled = false
	path := saveDoc(t, t.TempDir(), doc)

	channels := savedSection(t, readSavedJSON(t, path), "channels")
	native := savedBlock(t, channels, "native", "enabled")
	if native["enabled"] != false {
		t.Errorf("native.enabled = %v, want false", native["enabled"])
	}
	// Nothing else was customized, so native is the only channel block.
	if len(channels) != 1 {
		t.Errorf("expected only the native block, got channels: %v", keysOf(channels))
	}
}

func TestPrune_DisabledWebRoundTrip(t *testing.T) {
	dir := t.TempDir()

	doc := defaultEditableDocument()
	doc.Channels.Web.Enabled = false
	path := saveDoc(t, dir, doc)

	// Raw shape: exactly {"web":{"enabled":false}} inside channels.
	channels := savedSection(t, readSavedJSON(t, path), "channels")
	web := savedBlock(t, channels, "web", "enabled")
	if web["enabled"] != false {
		t.Errorf("web.enabled = %v, want false", web["enabled"])
	}

	// LoadEditableDocument -> ToConfig must keep the explicit false.
	loaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}
	if loaded.Channels.Web.Enabled {
		t.Error("loaded document web.enabled = true, want false (applyDefaults must not clobber an explicit false)")
	}
	cfg, err := loaded.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig failed: %v", err)
	}
	if cfg.Channels.Web.Enabled {
		t.Error("ToConfig web.enabled = true, want false")
	}

	// Native was never customized and is absent from the pruned file, so
	// LoadConfig must restore its code default (enabled: true).
	runtime, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !runtime.Channels.Native.Enabled {
		t.Error("LoadConfig on the pruned file: native.enabled = false, want true (default)")
	}
	if runtime.Channels.Web.Enabled {
		t.Error("LoadConfig on the pruned file: web.enabled = true, want false (explicitly saved)")
	}
}

func TestPrune_EnabledWebWritesNothing(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Channels.Native.Enabled = false // force a channels section
	path := saveDoc(t, t.TempDir(), doc)

	channels := savedSection(t, readSavedJSON(t, path), "channels")
	if _, ok := channels["web"]; ok {
		t.Errorf("web equals its default (enabled:true) and must write nothing, got %v", channels["web"])
	}
}

// --- pruning: per-channel keys ----------------------------------------------

func TestPrune_TelegramEnabledWithTokenWritesOnlyNeededKeys(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Channels.Telegram.Enabled = true
	doc.Channels.Telegram.Token = SecretValue{Mode: SecretModeLiteral, Value: "123456:abc-token"}
	path := saveDoc(t, t.TempDir(), doc)

	channels := savedSection(t, readSavedJSON(t, path), "channels")
	telegram := savedBlock(t, channels, "telegram", "enabled", "token")
	if telegram["enabled"] != true {
		t.Errorf("telegram.enabled = %v, want true", telegram["enabled"])
	}
	if telegram["token"] != "123456:abc-token" {
		t.Errorf("telegram.token = %v, want the literal token", telegram["token"])
	}
}

func TestPrune_WhatsAppDisabledWithCustomBridgeURLWritesOnlyBridgeURL(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Channels.WhatsApp.BridgeURL = "ws://bridge.example:9999"
	path := saveDoc(t, t.TempDir(), doc)

	channels := savedSection(t, readSavedJSON(t, path), "channels")
	whatsapp := savedBlock(t, channels, "whatsapp", "bridge_url")
	if whatsapp["bridge_url"] != "ws://bridge.example:9999" {
		t.Errorf("whatsapp.bridge_url = %v", whatsapp["bridge_url"])
	}
	if _, ok := whatsapp["enabled"]; ok {
		t.Error("whatsapp.enabled equals the default (false) and must not be written")
	}
}

func TestPrune_PlaceholderSecretAlwaysCountsAsDifference(t *testing.T) {
	// A keyring/env placeholder on an otherwise-default channel must be
	// written even though the channel is disabled and every plain field
	// matches the default.
	doc := defaultEditableDocument()
	doc.Channels.Discord.Token = SecretValue{Mode: SecretModeKeyring, SecretName: "discord.token"}
	path := saveDoc(t, t.TempDir(), doc)

	channels := savedSection(t, readSavedJSON(t, path), "channels")
	discord := savedBlock(t, channels, "discord", "token")
	if discord["token"] != "{{SECRET:discord.token}}" {
		t.Errorf("discord.token = %v, want {{SECRET:discord.token}}", discord["token"])
	}
}

func TestPrune_GatewayCustomizedWritesOnlyDiffingKeys(t *testing.T) {
	doc := defaultEditableDocument()
	def := DefaultConfig()
	// Change only the port; host stays at its default.
	doc.Gateway.Port = def.Gateway.Port + 1
	path := saveDoc(t, t.TempDir(), doc)

	parsed := readSavedJSON(t, path)
	gateway, ok := parsed["gateway"].(map[string]interface{})
	if !ok {
		t.Fatalf("gateway section missing, got %T", parsed["gateway"])
	}
	if len(gateway) != 1 {
		t.Fatalf("gateway keys = %v, want exactly [port]", keysOf(gateway))
	}
	if gateway["port"] != float64(def.Gateway.Port+1) {
		t.Errorf("gateway.port = %v, want %d", gateway["port"], def.Gateway.Port+1)
	}
}

// --- full round-trip pin -----------------------------------------------------

func TestPrune_RichDocumentFullRoundTrip(t *testing.T) {
	// Start from the runtime defaults, customize every channel and the
	// gateway (except QQ, which stays default and must vanish from the
	// file), then pin: save -> LoadEditableDocument -> ToConfig gives back
	// exactly the original runtime channels/gateway.
	cfg := DefaultConfig()

	cfg.Channels.Telegram = TelegramConfig{
		Enabled: true, Token: "tg-literal", Proxy: "http://proxy:3128",
		AllowFrom: FlexibleStringSlice{"12345"}, Verbose: VerboseBasic,
	}
	cfg.Channels.Discord = DiscordConfig{
		Enabled: true, Token: "dc-literal", AllowFrom: FlexibleStringSlice{"user#1"},
	}
	cfg.Channels.Feishu = FeishuConfig{
		Enabled: true, AppID: "feishu-app", AppSecret: "feishu-secret",
		EncryptKey: "feishu-enc", VerificationToken: "feishu-ver",
		AllowFrom: FlexibleStringSlice{"ou_1"},
	}
	cfg.Channels.Slack = SlackConfig{
		Enabled: true, BotToken: "xoxb-bot", AppToken: "xapp-workspace",
		AllowFrom: FlexibleStringSlice{"U123"},
	}
	cfg.Channels.LINE = LINEConfig{
		Enabled: true, ChannelSecret: "line-secret", ChannelAccessToken: "line-token",
		WebhookHost: "10.0.0.1", WebhookPort: 19090, WebhookPath: "/hook/line",
		AllowFrom: FlexibleStringSlice{"line-user"},
	}
	cfg.Channels.OneBot = OneBotConfig{
		Enabled: true, WSUrl: "ws://onebot:3001", AccessToken: "onebot-tok",
		ReconnectInterval: 9, GroupTriggerPrefix: []string{"@bot", "/lele"},
		AllowFrom: FlexibleStringSlice{"group1"},
	}
	// QQ intentionally left at defaults.
	cfg.Channels.DingTalk = DingTalkConfig{
		Enabled: true, ClientID: "dt-client", ClientSecret: "dt-secret",
		AllowFrom: FlexibleStringSlice{"dt-user"},
	}
	cfg.Channels.WhatsApp = WhatsAppConfig{
		Enabled: false, BridgeURL: "ws://wa-bridge:4000",
		AllowFrom: FlexibleStringSlice{"wa-user"},
	}
	cfg.Channels.MaixCam = MaixCamConfig{
		Enabled: true, Host: "192.168.1.50", Port: 18888,
		AllowFrom: FlexibleStringSlice{"cam1"},
	}
	// Customize native field-by-field so NativeConfig.LeleDir (a runtime-only
	// field the document does not carry) keeps its default on both sides.
	cfg.Channels.Native.Host = "0.0.0.0"
	cfg.Channels.Native.Port = 19000
	cfg.Channels.Native.TokenExpiryDays = 7
	cfg.Channels.Native.PinExpiryMinutes = 10
	cfg.Channels.Native.MaxClients = 9
	cfg.Channels.Native.CORSOrigins = []string{"http://example.local"}
	cfg.Channels.Native.SessionExpiryDays = 3
	cfg.Channels.Native.MaxUploadSizeMB = 12
	cfg.Channels.Native.UploadTTLHours = 2
	cfg.Channels.Web.Enabled = false
	cfg.Gateway = GatewayConfig{Host: "127.0.0.1", Port: 19790}

	doc := editableDocumentFromConfig(cfg)
	path := saveDoc(t, t.TempDir(), doc)

	// The untouched channel must be absent from the file entirely.
	channels := savedSection(t, readSavedJSON(t, path), "channels")
	if _, ok := channels["qq"]; ok {
		t.Errorf("default QQ channel must be pruned, got %v", channels["qq"])
	}

	loaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}
	got, err := loaded.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig failed: %v", err)
	}

	if !reflect.DeepEqual(got.Channels, cfg.Channels) {
		t.Errorf("channels round-trip mismatch:\n got=%#v\nwant=%#v", got.Channels, cfg.Channels)
	}
	if !reflect.DeepEqual(got.Gateway, cfg.Gateway) {
		t.Errorf("gateway round-trip mismatch:\n got=%#v\nwant=%#v", got.Gateway, cfg.Gateway)
	}
}

// --- web channel wiring ------------------------------------------------------

func TestWebChannel_DocumentExposesDefaultTrue(t *testing.T) {
	doc := editableDocumentFromConfig(DefaultConfig())
	if !doc.Channels.Web.Enabled {
		t.Error("editableDocumentFromConfig(defaults).Channels.Web.Enabled = false, want true")
	}
	if !defaultEditableDocument().Channels.Web.Enabled {
		t.Error("defaultEditableDocument().Channels.Web.Enabled = false, want true")
	}
}

func TestWebChannel_FileWithoutWebKeyKeepsDefaultTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"channels":{"telegram":{"enabled":true}}}`), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}
	if !doc.Channels.Web.Enabled {
		t.Error("file without a web key must keep the code default (enabled: true)")
	}
}

func TestWebChannel_FileWithExplicitFalseSurvivesLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"channels":{"web":{"enabled":false}}}`), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}
	if doc.Channels.Web.Enabled {
		t.Error("explicit web.enabled:false must survive LoadEditableDocument (applyDefaults must not clobber it)")
	}
}

// TestPrune_PromptCacheRoundTrip verifies that prompt_cache in agents.defaults
// survives a full prune round-trip (save minimal → load). This is a regression
// test for the bug where PromptCacheConfig was missing from
// EditableAgentDefaults, causing the WebUI to silently drop the setting on
// every save cycle.
func TestPrune_PromptCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()

	cfg := DefaultConfig()
	cfg.Agents.Defaults.PromptCache = PromptCacheConfig{Enabled: true, TTL: "1h"}
	doc := editableDocumentFromConfig(cfg)
	path := saveDoc(t, dir, doc)

	// The saved JSON must contain prompt_cache in agents.defaults.
	parsed := readSavedJSON(t, path)
	agentsRaw, ok := parsed["agents"]
	if !ok {
		t.Fatal("saved JSON missing agents section")
	}
	agents, ok := agentsRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("agents is %T, want object", agentsRaw)
	}
	defaultsRaw, ok := agents["defaults"]
	if !ok {
		t.Fatal("agents missing defaults")
	}
	defaults, ok := defaultsRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("agents.defaults is %T, want object", defaultsRaw)
	}
	pcRaw, ok := defaults["prompt_cache"]
	if !ok {
		t.Fatal("agents.defaults missing prompt_cache — field was silently dropped")
	}
	pc, ok := pcRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("prompt_cache is %T, want object", pcRaw)
	}
	if pc["enabled"] != true {
		t.Errorf("prompt_cache.enabled = %v, want true", pc["enabled"])
	}
	if pc["ttl"] != "1h" {
		t.Errorf("prompt_cache.ttl = %v, want \"1h\"", pc["ttl"])
	}

	// Reload via LoadEditableDocument → ToConfig and verify the field survived.
	loaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument failed: %v", err)
	}
	if loaded.Agents.Defaults.PromptCache.Enabled != true {
		t.Error("reloaded document prompt_cache.enabled = false, want true")
	}
	if loaded.Agents.Defaults.PromptCache.TTL != "1h" {
		t.Errorf("reloaded document prompt_cache.ttl = %q, want \"1h\"", loaded.Agents.Defaults.PromptCache.TTL)
	}

	// Also verify through the runtime Config path.
	runtime, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if runtime.Agents.Defaults.PromptCache.Enabled != true {
		t.Error("LoadConfig prompt_cache.enabled = false, want true")
	}
	if runtime.Agents.Defaults.PromptCache.TTL != "1h" {
		t.Errorf("LoadConfig prompt_cache.ttl = %q, want \"1h\"", runtime.Agents.Defaults.PromptCache.TTL)
	}
}

// TestPrune_AgentsDefaultsIncludedInRoundTrip extends the defaults round-trip
// safety net to verify that agents.defaults (including prompt_cache) survives
// a save + reload of the minimal file.
func TestPrune_AgentsDefaultsIncludedInRoundTrip(t *testing.T) {
	def := DefaultConfig()
	path := saveDoc(t, t.TempDir(), defaultEditableDocument())

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !reflect.DeepEqual(loaded.Agents.Defaults, def.Agents.Defaults) {
		t.Errorf("agents.defaults round-trip mismatch:\n got=%#v\nwant=%#v", loaded.Agents.Defaults, def.Agents.Defaults)
	}
}
