package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usememos/memogram/internal/ai"
	"github.com/usememos/memogram/internal/config"
	"github.com/usememos/memogram/internal/workflow"
)

func TestWebSetupEditPasswordAndPersistence(t *testing.T) {
	base, err := config.ReadNotebook("../../notebook.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base.Access, base.MemosToken = config.Access{UserIDs: []int64{42}}, "private-memos-token"
	path := filepath.Join(t.TempDir(), "web-settings.json")
	settings, err := OpenSettings(path, base)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(settings)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	client := server.Client()
	client.Jar, err = cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, endpoint string, value any, status int) []byte {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, server.URL+endpoint, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != status {
			t.Fatalf("%s %s: status %d, want %d: %s", method, endpoint, resp.StatusCode, status, data)
		}
		if strings.Contains(string(data), "private-memos-token") || strings.Contains(string(data), "private-ai-key") {
			t.Fatal("runtime credentials returned by API")
		}
		return data
	}
	if !bytes.Contains(request("GET", "/api/status", nil, 200), []byte(`"setup_required":true`)) {
		t.Fatal("first visit did not require a password")
	}
	request("GET", "/api/settings", nil, 401)
	request("POST", "/api/setup", map[string]string{"password": "short"}, 400)
	request("POST", "/api/setup", map[string]string{"password": "test-password-123"}, 200)
	request("POST", "/api/setup", map[string]string{"password": "replacement-password"}, 400)
	var view SettingsView
	if err := json.Unmarshal(request("GET", "/api/settings", nil, 200), &view); err != nil {
		t.Fatal(err)
	}
	provider := view.Notebook.Providers["cli_proxy"]
	// A real SDK request proves that web-saved addresses, models and keys are used.
	aiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-ai-key" || r.URL.Path != "/v1/responses" {
			t.Error("saved provider credentials or URL not used")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "web-selected-model" || !strings.Contains(body["instructions"].(string), "网页修改的系统提示词") {
			t.Error("saved model or prompt not used")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"web configured draft"}]}]}`)
	}))
	defer aiServer.Close()
	provider.BaseURL = aiServer.URL + "/v1"
	view.Notebook.Providers["cli_proxy"] = provider
	model := view.Notebook.Models["primary"]
	model.ID = "web-selected-model"
	view.Notebook.Models["primary"] = model
	view.Notebook.SystemPrompt = "网页修改的系统提示词"
	request("PUT", "/api/settings", map[string]any{"notebook": view.Notebook, "api_keys": map[string]string{"cli_proxy": "private-ai-key"}}, 200)
	result, err := ai.New(settings, nil).Generate(context.Background(), workflow.Request{Model: "primary", Profile: settings.Current().Profiles["tidy"]})
	if err != nil || result.Text != "web configured draft" {
		t.Fatalf("saved configuration not used: %+v %v", result, err)
	}
	if base.SystemPrompt == settings.Current().SystemPrompt {
		t.Fatal("startup YAML mutated")
	}
	reloaded, err := OpenSettings(path, base)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.CheckPassword("test-password-123") || reloaded.Current().Providers["cli_proxy"].APIKey != "private-ai-key" || reloaded.Current().MemosToken != base.MemosToken || !reloaded.Current().Access.Allows(42, "") {
		t.Fatal("credentials or settings lost on restart")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("settings file permissions: %v %v", info, err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || bytes.Contains(stored, []byte("test-password-123")) || bytes.Contains(stored, []byte("private-memos-token")) {
		t.Fatal("plain password or runtime Memos token stored")
	}
	// Invalid edits preserve the last working settings; blank key inputs preserve keys.
	view.Notebook.Models["primary"] = config.Model{Provider: "missing", ID: "bad", Protocol: "responses"}
	request("PUT", "/api/settings", map[string]any{"notebook": view.Notebook}, 400)
	if settings.Current().Models["primary"].ID != "web-selected-model" {
		t.Fatal("invalid configuration applied")
	}
	request("PUT", "/api/settings", map[string]any{"notebook": settings.Current(), "api_keys": map[string]string{"cli_proxy": ""}}, 200)
	if settings.Current().Providers["cli_proxy"].APIKey != "private-ai-key" {
		t.Fatal("blank key erased saved credential")
	}
	request("POST", "/api/password", map[string]string{"old_password": "wrong", "password": "new-password-456"}, 400)
	request("POST", "/api/password", map[string]string{"old_password": "test-password-123", "password": "new-password-456"}, 200)
	request("POST", "/api/logout", map[string]any{}, 200)
	request("GET", "/api/settings", nil, 401)
	request("POST", "/api/login", map[string]string{"password": "test-password-123"}, 401)
	request("POST", "/api/login", map[string]string{"password": "new-password-456"}, 200)
	// Rate limiting is exercised separately, rather than sleeping in this test.
	settings.mu.Lock()
	if settings.saved.AuthID == "" {
		t.Error("password identity not persisted")
	}
	settings.mu.Unlock()
	if !settings.CheckPassword("new-password-456") {
		t.Fatal("new password not applied")
	}
}

func TestWebOriginCookieAndLoginLimits(t *testing.T) {
	base, err := config.ReadNotebook("../../notebook.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := OpenSettings(filepath.Join(t.TempDir(), "settings.json"), base)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(settings)
	if err != nil {
		t.Fatal(err)
	}
	blocked := httptest.NewRequest(http.MethodPost, "https://settings.example.com/api/setup", strings.NewReader(`{"password":"test-password"}`))
	blocked.Header.Set("Origin", "https://attacker.example.com")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, blocked)
	if recorder.Code != 403 || settings.AuthID() != "" {
		t.Fatal("cross-origin setup allowed")
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "https://settings.example.com/api/setup", strings.NewReader(`{"password":"test-password"}`)))
	if recorder.Code != 200 {
		t.Fatalf("setup: %s", recorder.Body)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie attributes missing")
	}
	limitedSettings, err := OpenSettings(filepath.Join(t.TempDir(), "limited.json"), base)
	if err != nil {
		t.Fatal(err)
	}
	limited, err := NewHandler(limitedSettings)
	if err != nil {
		t.Fatal(err)
	}
	for range 11 {
		recorder = httptest.NewRecorder()
		limited.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "https://settings.example.com/api/login", strings.NewReader(`{"password":"wrong-password"}`)))
	}
	if recorder.Code != 429 {
		t.Fatalf("login attempt limiter missing: %d", recorder.Code)
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "https://settings.example.com/", nil))
	if page.Code != 200 || !strings.Contains(page.Body.String(), "设置管理密码") && !strings.Contains(page.Body.String(), "auth-title") {
		t.Fatal("embedded page missing")
	}
}
