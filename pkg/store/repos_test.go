package store

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestCronRepo_CRUD(t *testing.T) {
	s := openTestStore(t)
	repo := s.Cron()

	// List on empty table returns empty slice (not nil).
	jobs, err := repo.ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs() on empty table failed: %v", err)
	}
	if jobs == nil {
		t.Fatal("ListCronJobs() returned nil, want non-nil empty slice")
	}
	if len(jobs) != 0 {
		t.Fatalf("ListCronJobs() returned %d jobs, want 0", len(jobs))
	}

	// Get missing returns not found.
	if _, ok, err := repo.GetCronJob("missing"); err != nil || ok {
		t.Fatalf("GetCronJob(missing) = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	// Upsert a realistic row.
	row := &CronJobRow{
		ID:          "job-1",
		Name:        "daily heartbeat",
		Enabled:     true,
		Schedule:    `{"kind":"cron","expr":"0 9 * * *"}`,
		Payload:     `{"type":"spawn","spawn":{"task":"Perform heartbeat check","label":"heartbeat","agent_id":"coder"}}`,
		State:       `{"next_run_at":1754557200000,"last_status":"ok"}`,
		Scope:       "session",
		SessionKey:  "telegram:main",
		CreatedAtMS: 1754500000000,
		UpdatedAtMS: 1754500000000,
	}
	if err := repo.UpsertCronJob(row); err != nil {
		t.Fatalf("UpsertCronJob(job-1) failed: %v", err)
	}

	// Get and compare all fields.
	got, ok, err := repo.GetCronJob("job-1")
	if err != nil || !ok {
		t.Fatalf("GetCronJob(job-1) = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if !reflect.DeepEqual(got, row) {
		t.Errorf("GetCronJob(job-1) mismatch:\n got: %+v\nwant: %+v", got, row)
	}

	// Upsert again with changes.
	row.Enabled = false
	row.UpdatedAtMS = 1754600000000
	row.Payload = `{"type":"spawn","spawn":{"task":"Updated task","label":"updated","agent_id":"coder"}}`
	if err := repo.UpsertCronJob(row); err != nil {
		t.Fatalf("UpsertCronJob(job-1) update failed: %v", err)
	}
	got, ok, err = repo.GetCronJob("job-1")
	if err != nil || !ok {
		t.Fatalf("GetCronJob(job-1) after update = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got.Enabled != false {
		t.Errorf("Enabled after update = %v, want false", got.Enabled)
	}
	if got.UpdatedAtMS != 1754600000000 {
		t.Errorf("UpdatedAtMS after update = %d, want 1754600000000", got.UpdatedAtMS)
	}
	if got.Payload != row.Payload {
		t.Errorf("Payload after update = %q, want %q", got.Payload, row.Payload)
	}

	// Insert a second row.
	row2 := &CronJobRow{
		ID: "job-2", Name: "weekly report", Enabled: true,
		Schedule: `{"kind":"cron","expr":"0 10 * * 1"}`,
		Payload:  `{"type":"report"}`, State: `{}`,
		Scope: "global", SessionKey: "",
		CreatedAtMS: 1754700000000, UpdatedAtMS: 1754700000000,
	}
	if err := repo.UpsertCronJob(row2); err != nil {
		t.Fatalf("UpsertCronJob(job-2) failed: %v", err)
	}
	jobs, err = repo.ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs() after inserts failed: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("ListCronJobs() returned %d jobs, want 2", len(jobs))
	}

	// Delete one.
	if err := repo.DeleteCronJob("job-1"); err != nil {
		t.Fatalf("DeleteCronJob(job-1) failed: %v", err)
	}
	if _, ok, err := repo.GetCronJob("job-1"); err != nil || ok {
		t.Fatalf("GetCronJob(job-1) after delete = (_, %v, %v), want (_, false, nil)", ok, err)
	}
	jobs, err = repo.ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs() after delete failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("ListCronJobs() returned %d jobs, want 1", len(jobs))
	}

	// Delete non-existing is not an error.
	if err := repo.DeleteCronJob("job-1"); err != nil {
		t.Fatalf("DeleteCronJob(job-1) second time failed: %v", err)
	}

	// Delete all.
	if err := repo.DeleteAllCronJobs(); err != nil {
		t.Fatalf("DeleteAllCronJobs() failed: %v", err)
	}
	jobs, err = repo.ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs() after delete all failed: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("ListCronJobs() returned %d jobs, want 0", len(jobs))
	}
}

func TestGoalRepo_CRUD(t *testing.T) {
	s := openTestStore(t)
	repo := s.Goals()

	// Get missing.
	if _, ok, err := repo.GetGoal("missing"); err != nil || ok {
		t.Fatalf("GetGoal(missing) = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	// Set and get round-trip.
	goalJSON := `{"objective":"ship sqlite storage","status":"in_progress","progress":0.6,"updated_by":"agent"}`
	if err := repo.SetGoal("telegram:main", goalJSON); err != nil {
		t.Fatalf("SetGoal(telegram:main) failed: %v", err)
	}
	got, ok, err := repo.GetGoal("telegram:main")
	if err != nil || !ok {
		t.Fatalf("GetGoal(telegram:main) = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got != goalJSON {
		t.Errorf("GetGoal(telegram:main) = %q, want %q", got, goalJSON)
	}

	// Upsert overwrites.
	goalJSON2 := `{"objective":"ship sqlite storage","status":"done","progress":1.0,"updated_by":"agent"}`
	if err := repo.SetGoal("telegram:main", goalJSON2); err != nil {
		t.Fatalf("SetGoal(telegram:main) upsert failed: %v", err)
	}
	got, ok, err = repo.GetGoal("telegram:main")
	if err != nil || !ok {
		t.Fatalf("GetGoal(telegram:main) after upsert = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got != goalJSON2 {
		t.Errorf("GetGoal(telegram:main) after upsert = %q, want %q", got, goalJSON2)
	}

	// Second session key.
	if err := repo.SetGoal("discord:dev", `{"objective":"test"}`); err != nil {
		t.Fatalf("SetGoal(discord:dev) failed: %v", err)
	}
	goals, err := repo.ListGoals()
	if err != nil {
		t.Fatalf("ListGoals() failed: %v", err)
	}
	if len(goals) != 2 {
		t.Fatalf("ListGoals() returned %d entries, want 2", len(goals))
	}
	if goals["telegram:main"] != goalJSON2 {
		t.Errorf("ListGoals()[telegram:main] = %q, want %q", goals["telegram:main"], goalJSON2)
	}
	if goals["discord:dev"] != `{"objective":"test"}` {
		t.Errorf("ListGoals()[discord:dev] = %q, want %q", goals["discord:dev"], `{"objective":"test"}`)
	}

	// Delete.
	if err := repo.DeleteGoal("telegram:main"); err != nil {
		t.Fatalf("DeleteGoal(telegram:main) failed: %v", err)
	}
	if _, ok, err := repo.GetGoal("telegram:main"); err != nil || ok {
		t.Fatalf("GetGoal(telegram:main) after delete = (_, %v, %v), want (_, false, nil)", ok, err)
	}
	// Delete non-existing is not an error.
	if err := repo.DeleteGoal("telegram:main"); err != nil {
		t.Fatalf("DeleteGoal(telegram:main) second time failed: %v", err)
	}
}

func TestGroupRepo_CRUD(t *testing.T) {
	s := openTestStore(t)
	repo := s.Groups()

	// Get missing.
	if _, ok, err := repo.GetGroupState("missing"); err != nil || ok {
		t.Fatalf("GetGroupState(missing) = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	// Set and get round-trip.
	stateJSON := `{"mode":"round_robin","participants":["coder","writer"],"turn":3}`
	if err := repo.SetGroupState("group-1", stateJSON); err != nil {
		t.Fatalf("SetGroupState(group-1) failed: %v", err)
	}
	got, ok, err := repo.GetGroupState("group-1")
	if err != nil || !ok {
		t.Fatalf("GetGroupState(group-1) = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got != stateJSON {
		t.Errorf("GetGroupState(group-1) = %q, want %q", got, stateJSON)
	}

	// Upsert overwrites.
	stateJSON2 := `{"mode":"moderator","participants":["coder"],"turn":4}`
	if err := repo.SetGroupState("group-1", stateJSON2); err != nil {
		t.Fatalf("SetGroupState(group-1) upsert failed: %v", err)
	}
	got, ok, err = repo.GetGroupState("group-1")
	if err != nil || !ok {
		t.Fatalf("GetGroupState(group-1) after upsert = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got != stateJSON2 {
		t.Errorf("GetGroupState(group-1) after upsert = %q, want %q", got, stateJSON2)
	}

	// Second group.
	stateJSON3 := `{"mode":"pipeline","participants":["a","b"],"turn":1}`
	if err := repo.SetGroupState("group-2", stateJSON3); err != nil {
		t.Fatalf("SetGroupState(group-2) failed: %v", err)
	}
	states, err := repo.ListGroupStates()
	if err != nil {
		t.Fatalf("ListGroupStates() failed: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("ListGroupStates() returned %d entries, want 2", len(states))
	}
	if states["group-1"] != stateJSON2 {
		t.Errorf("ListGroupStates()[group-1] = %q, want %q", states["group-1"], stateJSON2)
	}
	if states["group-2"] != stateJSON3 {
		t.Errorf("ListGroupStates()[group-2] = %q, want %q", states["group-2"], stateJSON3)
	}

	// Delete.
	if err := repo.DeleteGroupState("group-1"); err != nil {
		t.Fatalf("DeleteGroupState(group-1) failed: %v", err)
	}
	if _, ok, err := repo.GetGroupState("group-1"); err != nil || ok {
		t.Fatalf("GetGroupState(group-1) after delete = (_, %v, %v), want (_, false, nil)", ok, err)
	}
}

func TestAuthRepo_CRUD(t *testing.T) {
	s := openTestStore(t)
	repo := s.Auth()

	// Get missing.
	if _, ok, err := repo.GetCredential("missing"); err != nil || ok {
		t.Fatalf("GetCredential(missing) = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	// Set and get round-trip.
	credJSON := `{"provider":"telegram","token":"REDACTED","expires_at":"2026-08-01T00:00:00Z"}`
	if err := repo.SetCredential("telegram:bot", credJSON); err != nil {
		t.Fatalf("SetCredential(telegram:bot) failed: %v", err)
	}
	got, ok, err := repo.GetCredential("telegram:bot")
	if err != nil || !ok {
		t.Fatalf("GetCredential(telegram:bot) = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got != credJSON {
		t.Errorf("GetCredential(telegram:bot) = %q, want %q", got, credJSON)
	}

	// Upsert overwrites.
	credJSON2 := `{"provider":"telegram","token":"NEW_TOKEN","expires_at":"2027-01-01T00:00:00Z"}`
	if err := repo.SetCredential("telegram:bot", credJSON2); err != nil {
		t.Fatalf("SetCredential(telegram:bot) upsert failed: %v", err)
	}
	got, ok, err = repo.GetCredential("telegram:bot")
	if err != nil || !ok {
		t.Fatalf("GetCredential(telegram:bot) after upsert = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got != credJSON2 {
		t.Errorf("GetCredential(telegram:bot) after upsert = %q, want %q", got, credJSON2)
	}

	// List.
	if err := repo.SetCredential("github:app", `{"provider":"github","token":"gh_token"}`); err != nil {
		t.Fatalf("SetCredential(github:app) failed: %v", err)
	}
	creds, err := repo.ListCredentials()
	if err != nil {
		t.Fatalf("ListCredentials() failed: %v", err)
	}
	if len(creds) != 2 {
		t.Fatalf("ListCredentials() returned %d entries, want 2", len(creds))
	}

	// Delete one.
	if err := repo.DeleteCredential("telegram:bot"); err != nil {
		t.Fatalf("DeleteCredential(telegram:bot) failed: %v", err)
	}
	if _, ok, err := repo.GetCredential("telegram:bot"); err != nil || ok {
		t.Fatalf("GetCredential(telegram:bot) after delete = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	// DeleteAll.
	if err := repo.DeleteAllCredentials(); err != nil {
		t.Fatalf("DeleteAllCredentials() failed: %v", err)
	}
	creds, err = repo.ListCredentials()
	if err != nil {
		t.Fatalf("ListCredentials() after delete all failed: %v", err)
	}
	if len(creds) != 0 {
		t.Fatalf("ListCredentials() returned %d entries, want 0", len(creds))
	}
}

func TestNativeClientRepo_CRUD(t *testing.T) {
	s := openTestStore(t)
	repo := s.NativeClients()

	// Get missing.
	if _, ok, err := repo.GetClient("missing"); err != nil || ok {
		t.Fatalf("GetClient(missing) = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	// Set and get round-trip.
	clientJSON := `{"id":"cli-abc","secret_hash":"$2a$10$xyz","allowed_scopes":["read","write"]}`
	if err := repo.SetClient("cli-abc", clientJSON); err != nil {
		t.Fatalf("SetClient(cli-abc) failed: %v", err)
	}
	got, ok, err := repo.GetClient("cli-abc")
	if err != nil || !ok {
		t.Fatalf("GetClient(cli-abc) = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got != clientJSON {
		t.Errorf("GetClient(cli-abc) = %q, want %q", got, clientJSON)
	}

	// Upsert overwrites client (created_at preserved).
	clientJSON2 := `{"id":"cli-abc","secret_hash":"$2a$10$new","allowed_scopes":["read","write","admin"]}`
	if err := repo.SetClient("cli-abc", clientJSON2); err != nil {
		t.Fatalf("SetClient(cli-abc) upsert failed: %v", err)
	}
	got, ok, err = repo.GetClient("cli-abc")
	if err != nil || !ok {
		t.Fatalf("GetClient(cli-abc) after upsert = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got != clientJSON2 {
		t.Errorf("GetClient(cli-abc) after upsert = %q, want %q", got, clientJSON2)
	}

	// List.
	if err := repo.SetClient("cli-xyz", `{"id":"cli-xyz","secret_hash":"$2a$10$abc"}`); err != nil {
		t.Fatalf("SetClient(cli-xyz) failed: %v", err)
	}
	clients, err := repo.ListClients()
	if err != nil {
		t.Fatalf("ListClients() failed: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("ListClients() returned %d entries, want 2", len(clients))
	}
	if clients["cli-abc"] != clientJSON2 {
		t.Errorf("ListClients()[cli-abc] = %q, want %q", clients["cli-abc"], clientJSON2)
	}

	// Delete one.
	if err := repo.DeleteClient("cli-abc"); err != nil {
		t.Fatalf("DeleteClient(cli-abc) failed: %v", err)
	}
	if _, ok, err := repo.GetClient("cli-abc"); err != nil || ok {
		t.Fatalf("GetClient(cli-abc) after delete = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	// DeleteAll.
	if err := repo.DeleteAllClients(); err != nil {
		t.Fatalf("DeleteAllClients() failed: %v", err)
	}
	clients, err = repo.ListClients()
	if err != nil {
		t.Fatalf("ListClients() after delete all failed: %v", err)
	}
	if len(clients) != 0 {
		t.Fatalf("ListClients() returned %d entries, want 0", len(clients))
	}
}

func TestSessionRepo_CRUD(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	// List on empty table returns empty slice.
	metas, err := repo.ListSessionMeta()
	if err != nil {
		t.Fatalf("ListSessionMeta() on empty table failed: %v", err)
	}
	if metas == nil {
		t.Fatal("ListSessionMeta() returned nil, want non-nil empty slice")
	}
	if len(metas) != 0 {
		t.Fatalf("ListSessionMeta() returned %d entries, want 0", len(metas))
	}

	// Get missing returns nil.
	meta, err := repo.GetSessionMeta("missing")
	if err != nil {
		t.Fatalf("GetSessionMeta(missing) error: %v", err)
	}
	if meta != nil {
		t.Fatalf("GetSessionMeta(missing) = %v, want nil", meta)
	}

	// Upsert a session.
	now := time.Now().Truncate(time.Microsecond)
	if err := repo.UpsertSession(SessionMeta{
		Key:           "test:session-1",
		Name:          "Test Session",
		Mode:          "agent",
		Summary:       "Test summary",
		VerboseLevel:  "full",
		Model:         "gpt-4",
		ThinkingLevel: "high",
		InputTokens:   100,
		OutputTokens:  50,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("UpsertSession() failed: %v", err)
	}

	// Get and compare.
	got, err := repo.GetSessionMeta("test:session-1")
	if err != nil {
		t.Fatalf("GetSessionMeta(test:session-1) error: %v", err)
	}
	if got == nil {
		t.Fatal("GetSessionMeta(test:session-1) = nil, want non-nil")
	}
	if got.Key != "test:session-1" {
		t.Errorf("Key = %q, want %q", got.Key, "test:session-1")
	}
	if got.Name != "Test Session" {
		t.Errorf("Name = %q, want %q", got.Name, "Test Session")
	}
	if got.Mode != "agent" {
		t.Errorf("Mode = %q, want %q", got.Mode, "agent")
	}
	if got.Summary != "Test summary" {
		t.Errorf("Summary = %q, want %q", got.Summary, "Test summary")
	}
	if got.VerboseLevel != "full" {
		t.Errorf("VerboseLevel = %q, want %q", got.VerboseLevel, "full")
	}
	if got.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", got.Model, "gpt-4")
	}
	if got.ThinkingLevel != "high" {
		t.Errorf("ThinkingLevel = %q, want %q", got.ThinkingLevel, "high")
	}
	if got.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want %d", got.InputTokens, 100)
	}
	if got.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want %d", got.OutputTokens, 50)
	}

	// Update metadata.
	if err := repo.UpsertSession(SessionMeta{
		Key:          "test:session-1",
		Name:         "Updated Name",
		Mode:         "chat",
		InputTokens:  200,
		OutputTokens: 100,
		CreatedAt:    now,
		UpdatedAt:    now.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertSession(update) failed: %v", err)
	}

	got, _ = repo.GetSessionMeta("test:session-1")
	if got.Name != "Updated Name" {
		t.Errorf("Name after update = %q, want %q", got.Name, "Updated Name")
	}
	if got.Mode != "chat" {
		t.Errorf("Mode after update = %q, want %q", got.Mode, "chat")
	}
	if got.InputTokens != 200 {
		t.Errorf("InputTokens after update = %d, want %d", got.InputTokens, 200)
	}

	// List sessions.
	metas, _ = repo.ListSessionMeta()
	if len(metas) != 1 {
		t.Fatalf("ListSessionMeta() returned %d entries, want 1", len(metas))
	}

	// Insert messages.
	msg1 := `{"role":"user","content":"Hello"}`
	if err := repo.InsertMessage("test:session-1", 0, "user", msg1, false); err != nil {
		t.Fatalf("InsertMessage(0) failed: %v", err)
	}
	msg2 := `{"role":"assistant","content":"Hi there!"}`
	if err := repo.InsertMessage("test:session-1", 1, "assistant", msg2, false); err != nil {
		t.Fatalf("InsertMessage(1) failed: %v", err)
	}

	// Load messages.
	msgs, err := repo.LoadMessages("test:session-1")
	if err != nil {
		t.Fatalf("LoadMessages() failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("LoadMessages() returned %d messages, want 2", len(msgs))
	}
	if msgs[0] != msg1 {
		t.Errorf("Message[0] = %q, want %q", msgs[0], msg1)
	}
	if msgs[1] != msg2 {
		t.Errorf("Message[1] = %q, want %q", msgs[1], msg2)
	}

	// Message count.
	count, err := repo.MessageCount("test:session-1")
	if err != nil {
		t.Fatalf("MessageCount() failed: %v", err)
	}
	if count != 2 {
		t.Errorf("MessageCount() = %d, want 2", count)
	}

	// Max seq.
	maxSeq, err := repo.MaxSeq("test:session-1")
	if err != nil {
		t.Fatalf("MaxSeq() failed: %v", err)
	}
	if maxSeq != 1 {
		t.Errorf("MaxSeq() = %d, want 1", maxSeq)
	}

	// Update message.
	msg3 := `{"role":"assistant","content":"Updated response"}`
	if err := repo.UpdateMessage("test:session-1", 1, "assistant", msg3, false); err != nil {
		t.Fatalf("UpdateMessage() failed: %v", err)
	}
	msgs, _ = repo.LoadMessages("test:session-1")
	if msgs[1] != msg3 {
		t.Errorf("Message[1] after update = %q, want %q", msgs[1], msg3)
	}

	// Delete last message.
	deletedSeq, err := repo.DeleteLastMessage("test:session-1")
	if err != nil {
		t.Fatalf("DeleteLastMessage() failed: %v", err)
	}
	if deletedSeq != 1 {
		t.Errorf("DeleteLastMessage() returned seq %d, want 1", deletedSeq)
	}
	count, _ = repo.MessageCount("test:session-1")
	if count != 1 {
		t.Errorf("MessageCount() after delete = %d, want 1", count)
	}

	// Add more messages for delete range test.
	for i := 1; i < 5; i++ {
		msgJSON := `{"role":"user","content":"msg"}`
		repo.InsertMessage("test:session-1", i, "user", msgJSON, false)
	}

	// Delete messages from seq 3 onwards.
	if err := repo.DeleteMessagesFrom("test:session-1", 3); err != nil {
		t.Fatalf("DeleteMessagesFrom(3) failed: %v", err)
	}
	count, _ = repo.MessageCount("test:session-1")
	if count != 3 {
		t.Errorf("MessageCount() after delete from = %d, want 3", count)
	}

	// Session exists.
	exists, err := repo.SessionExists("test:session-1")
	if err != nil {
		t.Fatalf("SessionExists() failed: %v", err)
	}
	if !exists {
		t.Error("SessionExists() = false, want true")
	}
	exists, _ = repo.SessionExists("nonexistent")
	if exists {
		t.Error("SessionExists(nonexistent) = true, want false")
	}

	// Delete session.
	if err := repo.DeleteSession("test:session-1"); err != nil {
		t.Fatalf("DeleteSession() failed: %v", err)
	}
	exists, _ = repo.SessionExists("test:session-1")
	if exists {
		t.Error("SessionExists() after delete = true, want false")
	}
}

func TestSessionRepo_ListByMode(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	now := time.Now()
	// Create sessions with different modes.
	repo.UpsertSession(SessionMeta{Key: "s1", Mode: "agent", CreatedAt: now, UpdatedAt: now})
	repo.UpsertSession(SessionMeta{Key: "s2", Mode: "chat", CreatedAt: now, UpdatedAt: now})
	repo.UpsertSession(SessionMeta{Key: "s3", Mode: "agent", CreatedAt: now, UpdatedAt: now})
	repo.UpsertSession(SessionMeta{Key: "s4", Mode: "", CreatedAt: now, UpdatedAt: now})

	// List by mode "agent".
	agents, err := repo.ListSessionMetaByMode("agent")
	if err != nil {
		t.Fatalf("ListSessionMetaByMode(agent) failed: %v", err)
	}
	if len(agents) != 2 {
		t.Errorf("ListSessionMetaByMode(agent) returned %d, want 2", len(agents))
	}

	// List by mode "chat".
	chats, err := repo.ListSessionMetaByMode("chat")
	if err != nil {
		t.Fatalf("ListSessionMetaByMode(chat) failed: %v", err)
	}
	if len(chats) != 1 {
		t.Errorf("ListSessionMetaByMode(chat) returned %d, want 1", len(chats))
	}

	// List by mode "" (should return "agent" sessions).
	empty, err := repo.ListSessionMetaByMode("")
	if err != nil {
		t.Fatalf("ListSessionMetaByMode('') failed: %v", err)
	}
	if len(empty) != 2 {
		t.Errorf("ListSessionMetaByMode('') returned %d, want 2", len(empty))
	}
}

func TestSessionRepo_PruneExcluded(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	now := time.Now()
	repo.UpsertSession(SessionMeta{Key: "test", CreatedAt: now, UpdatedAt: now})

	// Add 10 messages, mark first 7 as excluded.
	for i := 0; i < 10; i++ {
		excluded := i < 7
		msgJSON := `{"role":"user","content":"msg"}`
		repo.InsertMessage("test", i, "user", msgJSON, excluded)
	}

	// Prune to keep 5.
	pruned, err := repo.PruneExcluded("test", 5)
	if err != nil {
		t.Fatalf("PruneExcluded() failed: %v", err)
	}
	if pruned != 5 {
		t.Errorf("PruneExcluded() pruned %d, want 5", pruned)
	}

	count, _ := repo.MessageCount("test")
	if count != 5 {
		t.Errorf("MessageCount() after prune = %d, want 5", count)
	}
}

func TestSessionRepo_ReplaceMessages(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	now := time.Now()
	repo.UpsertSession(SessionMeta{Key: "rm-test", CreatedAt: now, UpdatedAt: now})

	// Insert initial 3 messages.
	for i := 0; i < 3; i++ {
		msgJSON := fmt.Sprintf(`{"role":"user","content":"msg%d"}`, i)
		repo.InsertMessage("rm-test", i, "user", msgJSON, false)
	}
	count, _ := repo.MessageCount("rm-test")
	if count != 3 {
		t.Fatalf("initial MessageCount = %d, want 3", count)
	}

	// Replace with 5 new messages (simulates a session save after adding messages).
	newRows := make([]MessageRow, 5)
	for i := range newRows {
		newRows[i] = MessageRow{
			Seq:  i,
			Role: "assistant",
			JSON: fmt.Sprintf(`{"role":"assistant","content":"new-%d"}`, i),
		}
	}
	if err := repo.ReplaceMessages("rm-test", newRows); err != nil {
		t.Fatalf("ReplaceMessages() failed: %v", err)
	}

	count, _ = repo.MessageCount("rm-test")
	if count != 5 {
		t.Errorf("MessageCount after replace = %d, want 5", count)
	}

	msgs, err := repo.LoadMessages("rm-test")
	if err != nil {
		t.Fatalf("LoadMessages() failed: %v", err)
	}
	for i, m := range msgs {
		want := fmt.Sprintf(`{"role":"assistant","content":"new-%d"}`, i)
		if m != want {
			t.Errorf("message[%d] = %q, want %q", i, m, want)
		}
	}

	// Replace with empty (clears all messages).
	if err := repo.ReplaceMessages("rm-test", nil); err != nil {
		t.Fatalf("ReplaceMessages(nil) failed: %v", err)
	}
	count, _ = repo.MessageCount("rm-test")
	if count != 0 {
		t.Errorf("MessageCount after clear = %d, want 0", count)
	}
}

func TestSessionRepo_AllMessageCounts(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	now := time.Now()

	// Create 3 sessions with different message counts
	repo.UpsertSession(SessionMeta{Key: "sess-a", CreatedAt: now, UpdatedAt: now})
	repo.UpsertSession(SessionMeta{Key: "sess-b", CreatedAt: now, UpdatedAt: now})
	repo.UpsertSession(SessionMeta{Key: "sess-c", CreatedAt: now, UpdatedAt: now})

	// sess-a: 2 messages
	for i := 0; i < 2; i++ {
		repo.InsertMessage("sess-a", i, "user",
			fmt.Sprintf(`{"role":"user","content":"a%d"}`, i), false)
	}
	// sess-b: 5 messages
	for i := 0; i < 5; i++ {
		repo.InsertMessage("sess-b", i, "assistant",
			fmt.Sprintf(`{"role":"assistant","content":"b%d"}`, i), false)
	}
	// sess-c: 0 messages (just metadata)

	counts, err := repo.AllMessageCounts()
	if err != nil {
		t.Fatalf("AllMessageCounts() failed: %v", err)
	}

	if counts["sess-a"] != 2 {
		t.Errorf("sess-a count = %d, want 2", counts["sess-a"])
	}
	if counts["sess-b"] != 5 {
		t.Errorf("sess-b count = %d, want 5", counts["sess-b"])
	}
	if counts["sess-c"] != 0 {
		t.Errorf("sess-c count = %d, want 0", counts["sess-c"])
	}
}
func TestSessionRepo_LoadMessagesBeforeSeq(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:before-seq"
	now := time.Now()
	if err := repo.UpsertSession(SessionMeta{Key: key, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("UpsertSession() failed: %v", err)
	}
	// Insert 5 messages (seq 0..4) with mixed excluded flags.
	messages := []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"m0"}`, Excluded: true},
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"m1"}`, Excluded: false},
		{Seq: 2, Role: "user", JSON: `{"role":"user","content":"m2"}`, Excluded: true},
		{Seq: 3, Role: "assistant", JSON: `{"role":"assistant","content":"m3"}`, Excluded: false},
		{Seq: 4, Role: "user", JSON: `{"role":"user","content":"m4"}`, Excluded: true},
	}
	if err := repo.InsertMessages(key, messages); err != nil {
		t.Fatalf("InsertMessages() failed: %v", err)
	}

	// LoadMessagesBeforeSeq(key, 3) returns exactly the JSON of seq 0,1,2 in order.
	got, err := repo.LoadMessagesBeforeSeq(key, 3)
	if err != nil {
		t.Fatalf("LoadMessagesBeforeSeq(key, 3) failed: %v", err)
	}
	want := []string{
		`{"role":"user","content":"m0"}`,
		`{"role":"assistant","content":"m1"}`,
		`{"role":"user","content":"m2"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadMessagesBeforeSeq(key, 3) = %v, want %v", got, want)
	}

	// LoadMessagesBeforeSeq(key, 0) returns empty (no error).
	empty, err := repo.LoadMessagesBeforeSeq(key, 0)
	if err != nil {
		t.Fatalf("LoadMessagesBeforeSeq(key, 0) failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("LoadMessagesBeforeSeq(key, 0) = %v, want empty", empty)
	}

	// LoadMessagesBeforeSeq on unknown session returns empty (no error).
	unknown, err := repo.LoadMessagesBeforeSeq("missing", 3)
	if err != nil {
		t.Fatalf("LoadMessagesBeforeSeq(missing, 3) failed: %v", err)
	}
	if len(unknown) != 0 {
		t.Errorf("LoadMessagesBeforeSeq(missing, 3) = %v, want empty", unknown)
	}
}

func TestSessionRepo_LoadMessagesFromSeq(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:from-seq"
	now := time.Now()
	if err := repo.UpsertSession(SessionMeta{Key: key, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("UpsertSession() failed: %v", err)
	}
	// Insert 5 messages (seq 0..4).
	messages := []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"m0"}`, Excluded: true},
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"m1"}`, Excluded: false},
		{Seq: 2, Role: "user", JSON: `{"role":"user","content":"m2"}`, Excluded: true},
		{Seq: 3, Role: "assistant", JSON: `{"role":"assistant","content":"m3"}`, Excluded: false},
		{Seq: 4, Role: "user", JSON: `{"role":"user","content":"m4"}`, Excluded: true},
	}
	if err := repo.InsertMessages(key, messages); err != nil {
		t.Fatalf("InsertMessages() failed: %v", err)
	}

	// LoadMessagesFromSeq(key, 2) returns exactly the seq>=2 JSON in order.
	got, err := repo.LoadMessagesFromSeq(key, 2)
	if err != nil {
		t.Fatalf("LoadMessagesFromSeq(key, 2) failed: %v", err)
	}
	want := []string{
		`{"role":"user","content":"m2"}`,
		`{"role":"assistant","content":"m3"}`,
		`{"role":"user","content":"m4"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadMessagesFromSeq(key, 2) = %v, want %v", got, want)
	}

	// LoadMessagesFromSeq(key, 0) returns all messages in order.
	all, err := repo.LoadMessagesFromSeq(key, 0)
	if err != nil {
		t.Fatalf("LoadMessagesFromSeq(key, 0) failed: %v", err)
	}
	wantAll := []string{
		`{"role":"user","content":"m0"}`,
		`{"role":"assistant","content":"m1"}`,
		`{"role":"user","content":"m2"}`,
		`{"role":"assistant","content":"m3"}`,
		`{"role":"user","content":"m4"}`,
	}
	if !reflect.DeepEqual(all, wantAll) {
		t.Errorf("LoadMessagesFromSeq(key, 0) = %v, want %v", all, wantAll)
	}

	// LoadMessagesFromSeq(key, 99) returns empty (no error).
	empty, err := repo.LoadMessagesFromSeq(key, 99)
	if err != nil {
		t.Fatalf("LoadMessagesFromSeq(key, 99) failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("LoadMessagesFromSeq(key, 99) = %v, want empty", empty)
	}
}

func TestSessionRepo_FirstInMemorySeq_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	// Upsert a session with an explicit FirstInMemorySeq.
	key := "test:first-in-mem"
	now := time.Now()
	if err := repo.UpsertSession(SessionMeta{
		Key:              key,
		CreatedAt:        now,
		UpdatedAt:        now,
		FirstInMemorySeq: 7,
	}); err != nil {
		t.Fatalf("UpsertSession() failed: %v", err)
	}

	got, err := repo.GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta() failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetSessionMeta() = nil, want non-nil")
	}
	if got.FirstInMemorySeq != 7 {
		t.Errorf("FirstInMemorySeq = %d, want 7", got.FirstInMemorySeq)
	}

	// A fresh session (no explicit set) reads back 0.
	freshKey := "test:first-in-mem-fresh"
	if err := repo.UpsertSession(SessionMeta{Key: freshKey, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("UpsertSession(fresh) failed: %v", err)
	}
	fresh, err := repo.GetSessionMeta(freshKey)
	if err != nil {
		t.Fatalf("GetSessionMeta(fresh) failed: %v", err)
	}
	if fresh == nil {
		t.Fatal("GetSessionMeta(fresh) = nil, want non-nil")
	}
	if fresh.FirstInMemorySeq != 0 {
		t.Errorf("fresh FirstInMemorySeq = %d, want 0", fresh.FirstInMemorySeq)
	}
}

func TestSessionRepo_UpdateFirstInMemorySeq(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:update-first-in-mem"
	now := time.Now()
	if err := repo.UpsertSession(SessionMeta{Key: key, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("UpsertSession() failed: %v", err)
	}

	if err := repo.UpdateFirstInMemorySeq(key, 5); err != nil {
		t.Fatalf("UpdateFirstInMemorySeq() failed: %v", err)
	}

	got, err := repo.GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta() failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetSessionMeta() = nil, want non-nil")
	}
	if got.FirstInMemorySeq != 5 {
		t.Errorf("FirstInMemorySeq = %d, want 5", got.FirstInMemorySeq)
	}
}

func TestSessionRepo_LoadMessagesWithSeq(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:with-seq"
	now := time.Now()
	if err := repo.UpsertSession(SessionMeta{Key: key, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("UpsertSession() failed: %v", err)
	}
	messages := []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"s0"}`, Excluded: true},
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"s1"}`, Excluded: false},
		{Seq: 2, Role: "user", JSON: `{"role":"user","content":"s2"}`, Excluded: true},
		{Seq: 3, Role: "assistant", JSON: `{"role":"assistant","content":"s3"}`, Excluded: false},
	}
	if err := repo.InsertMessages(key, messages); err != nil {
		t.Fatalf("InsertMessages() failed: %v", err)
	}

	got, err := repo.LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq() failed: %v", err)
	}
	want := []MessageRowFull{
		{Seq: 0, JSON: `{"role":"user","content":"s0"}`, Excluded: true},
		{Seq: 1, JSON: `{"role":"assistant","content":"s1"}`, Excluded: false},
		{Seq: 2, JSON: `{"role":"user","content":"s2"}`, Excluded: true},
		{Seq: 3, JSON: `{"role":"assistant","content":"s3"}`, Excluded: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadMessagesWithSeq() = %v, want %v", got, want)
	}

	// Empty session returns empty slice.
	empty, err := repo.LoadMessagesWithSeq("missing")
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq(missing) failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("LoadMessagesWithSeq(missing) = %v, want empty", empty)
	}
}

func TestSessionRepo_CountExcludedMessages(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:count-excluded"
	now := time.Now()
	if err := repo.UpsertSession(SessionMeta{Key: key, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("UpsertSession() failed: %v", err)
	}
	messages := []MessageRow{
		{Seq: 0, Role: "user", JSON: `{"role":"user","content":"c0"}`, Excluded: true},
		{Seq: 1, Role: "assistant", JSON: `{"role":"assistant","content":"c1"}`, Excluded: false},
		{Seq: 2, Role: "user", JSON: `{"role":"user","content":"c2"}`, Excluded: true},
		{Seq: 3, Role: "assistant", JSON: `{"role":"assistant","content":"c3"}`, Excluded: true},
		{Seq: 4, Role: "user", JSON: `{"role":"user","content":"c4"}`, Excluded: false},
	}
	if err := repo.InsertMessages(key, messages); err != nil {
		t.Fatalf("InsertMessages() failed: %v", err)
	}

	count, err := repo.CountExcludedMessages(key)
	if err != nil {
		t.Fatalf("CountExcludedMessages() failed: %v", err)
	}
	if count != 3 {
		t.Errorf("CountExcludedMessages() = %d, want 3", count)
	}

	// Empty session returns 0.
	emptyCount, err := repo.CountExcludedMessages("missing")
	if err != nil {
		t.Fatalf("CountExcludedMessages(missing) failed: %v", err)
	}
	if emptyCount != 0 {
		t.Errorf("CountExcludedMessages(missing) = %d, want 0", emptyCount)
	}
}
