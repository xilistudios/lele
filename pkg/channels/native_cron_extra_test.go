package channels

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/xilistudios/lele/pkg/cron"
)

// TestNativeCronList tests GET /api/v1/cron including include_disabled filter
// and job list/status response shape.
func TestNativeCronList(t *testing.T) {
	ts := newNativeTestServer(t)
	cs := attachCronService(t, ts)

	j1, err := cs.AddJob("job1", cron.CronSchedule{Kind: "every", EveryMS: ptrInt64(60000)}, "msg1", true, "", "")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	j2, err := cs.AddJob("job2", cron.CronSchedule{Kind: "every", EveryMS: ptrInt64(120000)}, "msg2", true, "", "")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	// Disable the second job.
	cs.EnableJob(j2.ID, false)
	_ = j1.ID

	t.Run("list enabled only by default", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodGet, "/api/v1/cron", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out struct {
			Jobs   []cron.CronJob         `json:"jobs"`
			Status map[string]interface{} `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out.Jobs) != 1 {
			t.Fatalf("expected 1 enabled job, got %d", len(out.Jobs))
		}
		if out.Status == nil {
			t.Error("status should be present")
		}
	})

	t.Run("list includes disabled when flag set", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodGet, "/api/v1/cron?include_disabled=true", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out struct {
			Jobs []cron.CronJob `json:"jobs"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out.Jobs) != 2 {
			t.Fatalf("expected 2 jobs, got %d", len(out.Jobs))
		}
	})
}

func TestNativeCronGet(t *testing.T) {
	ts := newNativeTestServer(t)
	cs := attachCronService(t, ts)

	job, err := cs.AddJob("job1", cron.CronSchedule{Kind: "every", EveryMS: ptrInt64(60000)}, "msg1", true, "", "")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	t.Run("get existing job", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodGet, "/api/v1/cron/"+job.ID, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		got := decodeCronJobResponse(t, resp)
		if got.ID != job.ID || got.Name != "job1" {
			t.Errorf("got job %+v", got)
		}
	})

	t.Run("get missing job returns 404", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodGet, "/api/v1/cron/nonexistent", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestNativeCronDelete(t *testing.T) {
	ts := newNativeTestServer(t)
	cs := attachCronService(t, ts)

	job, err := cs.AddJob("job1", cron.CronSchedule{Kind: "every", EveryMS: ptrInt64(60000)}, "msg1", true, "", "")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	t.Run("delete existing job", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodDelete, "/api/v1/cron/"+job.ID, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out struct {
			ID      string `json:"id"`
			Removed bool   `json:"removed"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !out.Removed || out.ID != job.ID {
			t.Errorf("got %+v", out)
		}
		if cs.GetJob(job.ID) != nil {
			t.Error("job should be removed from service")
		}
	})

	t.Run("delete missing job returns 404", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodDelete, "/api/v1/cron/nonexistent", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestNativeCronEnableDisableRun(t *testing.T) {
	ts := newNativeTestServer(t)
	attachCronService(t, ts)
	cs := ts.channel.cronService.(*cron.CronService)

	job, err := cs.AddJob("job1", cron.CronSchedule{Kind: "every", EveryMS: ptrInt64(60000)}, "msg1", true, "", "")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	t.Run("enable", func(t *testing.T) {
		cs.EnableJob(job.ID, false)
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron/"+job.ID+"/enable", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		got := decodeCronJobResponse(t, resp)
		if !got.Enabled {
			t.Error("job should be enabled")
		}
	})

	t.Run("enable missing job returns 404", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron/nonexistent/enable", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("disable", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron/"+job.ID+"/disable", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		got := decodeCronJobResponse(t, resp)
		if got.Enabled {
			t.Error("job should be disabled")
		}
	})

	t.Run("disable missing job returns 404", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron/nonexistent/disable", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("run job", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron/"+job.ID+"/run", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out struct {
			ID  string `json:"id"`
			Ran bool   `json:"ran"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !out.Ran || out.ID != job.ID {
			t.Errorf("got %+v", out)
		}
	})

	t.Run("run missing job returns 404", func(t *testing.T) {
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron/nonexistent/run", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

// TestNativeCronCreateErrorPaths exercises the error branches of handleCronCreate.
func TestNativeCronCreateErrorPaths(t *testing.T) {
	ts := newNativeTestServer(t)
	attachCronService(t, ts)

	t.Run("invalid body", func(t *testing.T) {
		resp := doCronRawRequest(t, ts, http.MethodPost, "/api/v1/cron", `not-json{`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("invalid spawn json", func(t *testing.T) {
		body := map[string]interface{}{
			"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
			"spawn":    "not-an-object",
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("missing message/command/spawn", func(t *testing.T) {
		body := map[string]interface{}{
			"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("missing schedule kind", func(t *testing.T) {
		body := map[string]interface{}{
			"message":  "hello",
			"schedule": map[string]interface{}{},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("invalid message field", func(t *testing.T) {
		body := map[string]interface{}{
			"message":  12345,
			"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
		}
		resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
}

// TestNativeCronCreateCommand exercises creating a command job which then
// triggers the UpdateJob path inside handleCronCreate.
func TestNativeCronCreateCommand(t *testing.T) {
	ts := newNativeTestServer(t)
	attachCronService(t, ts)

	body := map[string]interface{}{
		"name":     "cmd job",
		"schedule": map[string]interface{}{"kind": "every", "everyMs": 60000},
		"command":  "ls -la",
		"channel":  "native",
		"to":       "someone",
	}
	resp := doCronRequest(t, ts, http.MethodPost, "/api/v1/cron", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	job := decodeCronJobResponse(t, resp)
	if job.Payload.Command != "ls -la" {
		t.Errorf("command = %q", job.Payload.Command)
	}
	if job.Payload.Deliver {
		t.Error("deliver should be false when command set")
	}
}

// doCronRawRequest sends a raw body string to the cron endpoint.
func doCronRawRequest(t *testing.T, ts *nativeTestServer, method, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.server.URL+path, bytes.NewBufferString(body))
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
