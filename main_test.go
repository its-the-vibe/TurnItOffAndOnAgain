package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// --- getEnv ---

func TestGetEnv_Default(t *testing.T) {
	got := getEnv("DEFINITELY_NOT_SET_VAR_XYZ", "default")
	if got != "default" {
		t.Errorf("expected %q, got %q", "default", got)
	}
}

func TestGetEnv_Set(t *testing.T) {
	t.Setenv("TEST_ENV_KEY", "hello")
	got := getEnv("TEST_ENV_KEY", "default")
	if got != "hello" {
		t.Errorf("expected %q, got %q", "hello", got)
	}
}

// --- loadConfig ---

func TestLoadConfig_Valid(t *testing.T) {
	data := `[{"service":"svc","dir":"/tmp","upCommands":["up"],"downCommands":["down"]}]`
	f, err := os.CreateTemp(t.TempDir(), "projects*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	orig := configFile
	configFile = f.Name()
	t.Cleanup(func() { configFile = orig })

	if err := loadConfig(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := projects["svc"]; !ok {
		t.Error("expected project 'svc' to be loaded")
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	orig := configFile
	configFile = "/nonexistent/path/projects.json"
	t.Cleanup(func() { configFile = orig })

	if err := loadConfig(); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "projects*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not-json"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	orig := configFile
	configFile = f.Name()
	t.Cleanup(func() { configFile = orig })

	if err := loadConfig(); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --- handlePostMessage ---

func TestHandlePostMessage_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/messages", nil)
	rec := httptest.NewRecorder()
	handlePostMessage(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandlePostMessage_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBufferString("bad json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlePostMessage(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandlePostMessage_MissingActionField(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"foo": "bar"})
	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlePostMessage(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandlePostMessage_Success(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	orig := redisClient
	redisClient = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { redisClient = orig })

	origProjects := projects
	projects = map[string]Project{
		"my-svc": {
			Service:      "my-svc",
			Dir:          "/tmp",
			UpCommands:   []string{"docker compose up -d"},
			DownCommands: []string{"docker compose down"},
		},
	}
	t.Cleanup(func() { projects = origProjects })

	body, _ := json.Marshal(map[string]string{"up": "my-svc"})
	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlePostMessage(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- processMessage ---

func TestProcessMessage_InvalidJSON(t *testing.T) {
	err := processMessage(context.TODO(), nil, "not-json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestProcessMessage_MissingAction(t *testing.T) {
	msg, _ := json.Marshal(RedisMessage{})
	err := processMessage(context.TODO(), nil, string(msg))
	if err == nil {
		t.Error("expected error for missing action")
	}
}

func TestProcessMessage_ServiceNotFound(t *testing.T) {
	origProjects := projects
	projects = map[string]Project{}
	t.Cleanup(func() { projects = origProjects })

	msg, _ := json.Marshal(RedisMessage{Up: "unknown-svc"})
	err := processMessage(context.TODO(), nil, string(msg))
	if err != nil {
		t.Errorf("expected nil error for missing service, got %v", err)
	}
}

func TestProcessMessage_NoRestartCommands(t *testing.T) {
	origProjects := projects
	projects = map[string]Project{
		"svc": {Service: "svc", Dir: "/tmp", UpCommands: []string{"up"}, DownCommands: []string{"down"}},
	}
	t.Cleanup(func() { projects = origProjects })

	msg, _ := json.Marshal(RedisMessage{Restart: "svc"})
	err := processMessage(context.TODO(), nil, string(msg))
	if err == nil {
		t.Error("expected error when restartCommands not configured")
	}
}

func TestProcessMessage_Up(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	origProjects := projects
	origTargetQueue := defaultTargetQueue
	projects = map[string]Project{
		"svc": {Service: "svc", Dir: "/tmp", UpCommands: []string{"docker compose up -d"}, DownCommands: []string{"docker compose down"}},
	}
	defaultTargetQueue = "poppit:notifications"
	t.Cleanup(func() {
		projects = origProjects
		defaultTargetQueue = origTargetQueue
	})

	msg, _ := json.Marshal(RedisMessage{Up: "svc"})
	if err := processMessage(testContext(t), rdb, string(msg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, err := mr.DB(0).List("poppit:notifications")
	if err != nil {
		t.Fatalf("failed to read list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 notification in queue, got %d", len(items))
	}

	var notif PoppitNotification
	if err := json.Unmarshal([]byte(items[0]), &notif); err != nil {
		t.Fatalf("failed to parse notification: %v", err)
	}
	if notif.Repo != "svc" {
		t.Errorf("expected repo 'svc', got %q", notif.Repo)
	}
	if notif.Type != "service-up" {
		t.Errorf("expected type 'service-up', got %q", notif.Type)
	}
}

func TestProcessMessage_Down(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	origProjects := projects
	origTargetQueue := defaultTargetQueue
	projects = map[string]Project{
		"svc": {Service: "svc", Dir: "/tmp", UpCommands: []string{"up"}, DownCommands: []string{"docker compose down"}},
	}
	defaultTargetQueue = "poppit:notifications"
	t.Cleanup(func() {
		projects = origProjects
		defaultTargetQueue = origTargetQueue
	})

	msg, _ := json.Marshal(RedisMessage{Down: "svc"})
	if err := processMessage(testContext(t), rdb, string(msg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, err := mr.DB(0).List("poppit:notifications")
	if err != nil {
		t.Fatalf("failed to read list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 notification in queue, got %d", len(items))
	}

	var notif PoppitNotification
	if err := json.Unmarshal([]byte(items[0]), &notif); err != nil {
		t.Fatalf("failed to parse notification: %v", err)
	}
	if notif.Type != "service-down" {
		t.Errorf("expected type 'service-down', got %q", notif.Type)
	}
}

func TestProcessMessage_Restart(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	origProjects := projects
	origTargetQueue := defaultTargetQueue
	projects = map[string]Project{
		"svc": {
			Service:         "svc",
			Dir:             "/tmp",
			UpCommands:      []string{"up"},
			DownCommands:    []string{"down"},
			RestartCommands: []string{"docker compose restart"},
		},
	}
	defaultTargetQueue = "poppit:notifications"
	t.Cleanup(func() {
		projects = origProjects
		defaultTargetQueue = origTargetQueue
	})

	msg, _ := json.Marshal(RedisMessage{Restart: "svc"})
	if err := processMessage(testContext(t), rdb, string(msg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, err := mr.DB(0).List("poppit:notifications")
	if err != nil {
		t.Fatalf("failed to read list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 notification in queue, got %d", len(items))
	}

	var notif PoppitNotification
	if err := json.Unmarshal([]byte(items[0]), &notif); err != nil {
		t.Fatalf("failed to parse notification: %v", err)
	}
	if notif.Type != "service-restart" {
		t.Errorf("expected type 'service-restart', got %q", notif.Type)
	}
}

func TestProcessMessage_CustomTargetQueue(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	origProjects := projects
	origTargetQueue := defaultTargetQueue
	projects = map[string]Project{
		"svc": {Service: "svc", Dir: "/tmp", UpCommands: []string{"up"}, DownCommands: []string{"down"}},
	}
	defaultTargetQueue = "poppit:notifications"
	t.Cleanup(func() {
		projects = origProjects
		defaultTargetQueue = origTargetQueue
	})

	msg, _ := json.Marshal(RedisMessage{Up: "svc", TargetQueue: "custom:queue"})
	if err := processMessage(testContext(t), rdb, string(msg)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mr.DB(0).Exists("poppit:notifications") {
		t.Error("expected no items in default queue")
	}
	items, err := mr.DB(0).List("custom:queue")
	if err != nil {
		t.Fatalf("failed to read list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item in custom queue, got %d", len(items))
	}
}
