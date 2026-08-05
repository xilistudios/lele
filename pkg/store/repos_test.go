package store

import (
	"reflect"
	"testing"
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
