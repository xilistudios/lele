package channels

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/cron"
)

// attachCronService wires a real cron service (backed by a temp store) into
// the test server's native channel so the cron REST endpoints are usable.
func attachCronService(t *testing.T, ts *nativeTestServer) *cron.CronService {
	t.Helper()
	cs := cron.NewCronService(filepath.Join(t.TempDir(), "cron.json"), nil)
	ts.channel.SetCronService(cs)
	return cs
}

func doCronRequest(t *testing.T, ts *nativeTestServer, method, path string, body interface{}) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, ts.server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decodeCronJobResponse(t *testing.T, resp *http.Response) cron.CronJob {
	t.Helper()
	defer resp.Body.Close()
	var out struct {
		Job cron.CronJob `json:"job"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out.Job
}

func TestCronCreateWithSpawn(t *testing.T) {
	ts := newNativeTestServer(t)
	attachCronService(t, ts)

	t.Run("create job with spawn config", func(t *testing.T) {
		body := map[string]interface{}{
			"name": "nightly report",
			"schedule": map[string]interface{}{
				"kind": "cron",
				"expr": "0 9 * * *",
			},
			"spawn": map[string]interface{}{
				"task":     "Generate the daily report",
				"label":    "daily-report",
				"agent_id": "main",
				"guidance": "Be concise",
			},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
		job := decodeCronJobResponse(t, resp)

		if job.Payload.Spawn == nil {
			t.Fatal("spawn config not persisted")
		}
		if job.Payload.Spawn.Task != "Generate the daily report" {
			t.Errorf("spawn.task = %q", job.Payload.Spawn.Task)
		}
		if job.Payload.Spawn.Label != "daily-report" {
			t.Errorf("spawn.label = %q", job.Payload.Spawn.Label)
		}
		if job.Payload.Spawn.AgentID != "main" {
			t.Errorf("spawn.agent_id = %q", job.Payload.Spawn.AgentID)
		}
		if job.Payload.Spawn.Guidance != "Be concise" {
			t.Errorf("spawn.guidance = %q", job.Payload.Spawn.Guidance)
		}
		if job.Payload.Deliver {
			t.Error("deliver should be forced false when spawn is set")
		}
	})

	t.Run("spawn without agent_id defaults to empty", func(t *testing.T) {
		body := map[string]interface{}{
			"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
			"spawn":    map[string]interface{}{"task": "Do something"},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
		job := decodeCronJobResponse(t, resp)
		if job.Payload.Spawn == nil || job.Payload.Spawn.Task != "Do something" {
			t.Fatal("spawn config not persisted")
		}
		if job.Payload.Spawn.AgentID != "" {
			t.Errorf("spawn.agent_id = %q, want empty", job.Payload.Spawn.AgentID)
		}
	})

	t.Run("spawn with model override is persisted", func(t *testing.T) {
		body := map[string]interface{}{
			"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
			"spawn": map[string]interface{}{
				"task":  "Summarize news",
				"model": "anthropic:claude-sonnet",
			},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
		job := decodeCronJobResponse(t, resp)
		if job.Payload.Spawn == nil || job.Payload.Spawn.Model != "anthropic:claude-sonnet" {
			t.Fatalf("spawn.model = %q, want %q", job.Payload.Spawn.Model, "anthropic:claude-sonnet")
		}
	})

	t.Run("spawn with bare model name is accepted", func(t *testing.T) {
		body := map[string]interface{}{
			"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
			"spawn": map[string]interface{}{
				"task":  "Summarize news",
				"model": "gpt-4o",
			},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
		job := decodeCronJobResponse(t, resp)
		if job.Payload.Spawn == nil || job.Payload.Spawn.Model != "gpt-4o" {
			t.Fatalf("spawn.model = %q, want %q", job.Payload.Spawn.Model, "gpt-4o")
		}
	})

	t.Run("spawn with unknown provider in model is rejected", func(t *testing.T) {
		body := map[string]interface{}{
			"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
			"spawn": map[string]interface{}{
				"task":  "Summarize news",
				"model": "nonexistent:some-model",
			},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("spawn with empty task rejected", func(t *testing.T) {
		body := map[string]interface{}{
			"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
			"spawn":    map[string]interface{}{"task": "   "},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("spawn with unknown agent rejected", func(t *testing.T) {
		body := map[string]interface{}{
			"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
			"spawn":    map[string]interface{}{"task": "Do something", "agent_id": "ghost"},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("spawn-only job requires no message", func(t *testing.T) {
		body := map[string]interface{}{
			"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
			"spawn":    map[string]interface{}{"task": "Task only"},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
		job := decodeCronJobResponse(t, resp)
		if job.Name == "" {
			t.Error("job name should default to spawn task")
		}
	})
}

func TestCronUpdateSpawn(t *testing.T) {
	ts := newNativeTestServer(t)
	cs := attachCronService(t, ts)

	job, err := cs.AddJob("base", cron.CronSchedule{Kind: "every", EveryMS: ptrInt64(60000)}, "hello", true, "", "")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	t.Run("set spawn on existing job", func(t *testing.T) {
		body := map[string]interface{}{
			"spawn": map[string]interface{}{"task": "New task", "agent_id": "main"},
		}
		resp := doCronRequest(t, ts, http.MethodPut, "/api/v1/cron/"+job.ID, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		updated := decodeCronJobResponse(t, resp)
		if updated.Payload.Spawn == nil || updated.Payload.Spawn.Task != "New task" {
			t.Fatal("spawn not set on update")
		}
		if updated.Payload.Deliver {
			t.Error("deliver should be forced false when spawn is set")
		}
	})

	t.Run("clear spawn with explicit null", func(t *testing.T) {
		// Build raw JSON body so "spawn": null is preserved.
		raw := []byte(`{"spawn": null}`)
		req, err := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/cron/"+job.ID, bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+ts.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		updated := decodeCronJobResponse(t, resp)
		if updated.Payload.Spawn != nil {
			t.Fatal("spawn should be cleared")
		}
	})

	t.Run("omitted spawn leaves existing untouched", func(t *testing.T) {
		// Re-add spawn first.
		body := map[string]interface{}{
			"spawn": map[string]interface{}{"task": "Keep me"},
		}
		resp := doCronRequest(t, ts, http.MethodPut, "/api/v1/cron/"+job.ID, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("set spawn status = %d", resp.StatusCode)
		}
		resp.Body.Close()

		// Update only the name.
		resp = doCronRequest(t, ts, http.MethodPut, "/api/v1/cron/"+job.ID, map[string]interface{}{"name": "renamed"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		updated := decodeCronJobResponse(t, resp)
		if updated.Payload.Spawn == nil || updated.Payload.Spawn.Task != "Keep me" {
			t.Fatal("spawn should be preserved when omitted")
		}
	})

	t.Run("convert message job to spawn job by clearing message", func(t *testing.T) {
		raw := []byte(`{"message": null, "spawn": {"task": "Converted"}}`)
		req, err := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/cron/"+job.ID, bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+ts.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		updated := decodeCronJobResponse(t, resp)
		if updated.Payload.Message != "" {
			t.Errorf("message = %q, want cleared", updated.Payload.Message)
		}
		if updated.Payload.Spawn == nil || updated.Payload.Spawn.Task != "Converted" {
			t.Fatal("spawn not set")
		}
	})

	t.Run("clearing all actions rejected", func(t *testing.T) {
		raw := []byte(`{"message": null, "spawn": null}`)
		req, err := http.NewRequest(http.MethodPut, ts.server.URL+"/api/v1/cron/"+job.ID, bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+ts.token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})
}

func ptrInt64(v int64) *int64 { return &v }
