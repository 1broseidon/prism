package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeRuntime struct {
	spawnFn   func(context.Context, SpawnRequest) (*SpawnResult, error)
	stopFn    func(context.Context, string) error
	statusFn  func(context.Context, string) (*RuntimeStatus, error)
	cleanupFn func(context.Context) error
}

func (f *fakeRuntime) Spawn(ctx context.Context, req SpawnRequest) (*SpawnResult, error) {
	if f.spawnFn != nil {
		return f.spawnFn(ctx, req)
	}
	return nil, nil
}

func (f *fakeRuntime) Stop(ctx context.Context, id string) error {
	if f.stopFn != nil {
		return f.stopFn(ctx, id)
	}
	return nil
}

func (f *fakeRuntime) Status(ctx context.Context, id string) (*RuntimeStatus, error) {
	if f.statusFn != nil {
		return f.statusFn(ctx, id)
	}
	return &RuntimeStatus{Status: "running"}, nil
}

func (f *fakeRuntime) Cleanup(ctx context.Context) error {
	if f.cleanupFn != nil {
		return f.cleanupFn(ctx)
	}
	return nil
}

func TestManagerSpawnAndList(t *testing.T) {
	runtime := &fakeRuntime{spawnFn: func(_ context.Context, req SpawnRequest) (*SpawnResult, error) {
		return &SpawnResult{
			Endpoint: "/mcp/" + req.ID,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("ok"))
			}),
			Tools:   []string{"ping", "pong"},
			Status:  "running",
			Runtime: "process",
		}, nil
	}}
	manager := NewManager(runtime, 2, testLogger(t))
	mux := http.NewServeMux()
	manager.RegisterRoutes(mux)

	spawnReq := httptest.NewRequest(http.MethodPost, "/manage/spawn", mustJSON(t, SpawnRequest{ID: "github", Command: "echo"}))
	spawnRec := httptest.NewRecorder()
	mux.ServeHTTP(spawnRec, spawnReq)
	if spawnRec.Code != http.StatusCreated {
		t.Fatalf("spawn status = %d, body = %s", spawnRec.Code, spawnRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/manage", nil)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d", listRec.Code)
	}
	var payload struct {
		Backends []ManagedBackend `json:"backends"`
		Count    int              `json:"count"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 1 || len(payload.Backends) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Backends[0].ID != "github" {
		t.Fatalf("backend id = %q", payload.Backends[0].ID)
	}
}

func TestManagerMaxBackendsLimit(t *testing.T) {
	manager := NewManager(&fakeRuntime{spawnFn: func(_ context.Context, req SpawnRequest) (*SpawnResult, error) {
		return &SpawnResult{Endpoint: "/mcp/" + req.ID, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), Status: "running"}, nil
	}}, 1, testLogger(t))
	mux := http.NewServeMux()
	manager.RegisterRoutes(mux)

	for i, want := range []int{http.StatusCreated, http.StatusServiceUnavailable} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manage/spawn", mustJSON(t, SpawnRequest{ID: string(rune('a' + i)), Command: "echo"}))
		mux.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d got %d want %d body=%s", i, rec.Code, want, rec.Body.String())
		}
	}
}

func TestManagerDeleteStopsBackend(t *testing.T) {
	stopped := false
	manager := NewManager(&fakeRuntime{
		spawnFn: func(_ context.Context, req SpawnRequest) (*SpawnResult, error) {
			return &SpawnResult{Endpoint: "/mcp/" + req.ID, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), Status: "running"}, nil
		},
		stopFn: func(_ context.Context, id string) error {
			if id == "github" {
				stopped = true
			}
			return nil
		},
	}, 2, testLogger(t))
	mux := http.NewServeMux()
	manager.RegisterRoutes(mux)

	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/manage/spawn", mustJSON(t, SpawnRequest{ID: "github", Command: "echo"})))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/manage/github", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !stopped {
		t.Fatal("expected runtime stop to be called")
	}
}

func TestManagerMCPProxyDispatch(t *testing.T) {
	manager := NewManager(&fakeRuntime{spawnFn: func(_ context.Context, req SpawnRequest) (*SpawnResult, error) {
		return &SpawnResult{
			Endpoint: "/mcp/" + req.ID,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(r.URL.Path))
			}),
			Status: "running",
		}, nil
	}}, 2, testLogger(t))
	mux := http.NewServeMux()
	manager.RegisterRoutes(mux)
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/manage/spawn", mustJSON(t, SpawnRequest{ID: "github", Command: "echo"})))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp/github/messages", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d", rec.Code)
	}
	if rec.Body.String() != "/messages" {
		t.Fatalf("proxy path = %q", rec.Body.String())
	}
}

func mustJSON(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(data)
}
