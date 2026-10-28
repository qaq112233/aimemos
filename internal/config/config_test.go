package config

import (
	"reflect"
	"strings"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestFromEnv(t *testing.T) {
	cfg, err := FromEnv(env(map[string]string{
		"SERVER_ADDR":       "dns:localhost:5230/",
		"BOT_TOKEN":         " 123:token ",
		"BOT_PROXY_ADDR":    "http://telegram-proxy:8081",
		"ALLOWED_USERNAMES": " Alice, ,bob ,",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{
		ServerAddr:   "http://localhost:5230",
		BotToken:     "123:token",
		BotProxyAddr: "http://telegram-proxy:8081",
		DataPath:     DefaultDataPath,
		Access:       Access{Usernames: []string{"alice", "bob"}},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestFromEnvRejectsInvalidSettings(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		values     map[string]string
	}{
		{"missing server", "SERVER_ADDR is required", map[string]string{"BOT_TOKEN": "t", "ALLOWED_USER_IDS": "42"}},
		{"blank server", "SERVER_ADDR is required", map[string]string{"SERVER_ADDR": " ", "BOT_TOKEN": "t", "ALLOWED_USER_IDS": "42"}},
		{"invalid server", "SERVER_ADDR must be an HTTP", map[string]string{"SERVER_ADDR": "ftp://memos", "BOT_TOKEN": "t", "ALLOWED_USER_IDS": "42"}},
		{"missing token", "BOT_TOKEN is required", map[string]string{"SERVER_ADDR": "localhost:5230"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := FromEnv(env(tc.values)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestFromEnvKeepsDataPath(t *testing.T) {
	cfg, err := FromEnv(env(map[string]string{"SERVER_ADDR": "localhost", "BOT_TOKEN": "t", "ALLOWED_USER_IDS": "42", "DATA": "/data/data.txt"}))
	if err != nil || cfg.DataPath != "/data/data.txt" || cfg.Access.Usernames != nil {
		t.Fatalf("got %+v, %v", cfg, err)
	}
}

func TestMemosCloudflareServiceToken(t *testing.T) {
	for _, tc := range []struct {
		name, id, secret string
		wantError        bool
	}{
		{"disabled", "", "", false},
		{"enabled", " test-id.access ", " test-secret ", false},
		{"missing secret", "test-id.access", "", true},
		{"missing id", "", "test-secret", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := FromEnv(env(map[string]string{
				"SERVER_ADDR": "https://memos.example.com", "BOT_TOKEN": "t", "ALLOWED_USER_IDS": "42",
				"MEMOS_CF_ACCESS_CLIENT_ID": tc.id, "MEMOS_CF_ACCESS_CLIENT_SECRET": tc.secret,
			}))
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "must be set together") || strings.Contains(err.Error(), "test-secret") {
					t.Fatalf("missing credential accepted or leaked: %v", err)
				}
				return
			}
			if err != nil || cfg.MemosCFClientID != strings.TrimSpace(tc.id) || cfg.MemosCFClientSecret != strings.TrimSpace(tc.secret) {
				t.Fatalf("service token configuration: %v", err)
			}
		})
	}
}

func TestProxyEnvironmentPrecedenceAndValidation(t *testing.T) {
	for _, tc := range []struct{ shared, telegram, want string }{
		{"", "", ""},
		{"http://shared.test:8080", "", "http://shared.test:8080"},
		{"http://shared.test:8080", "socks5://telegram.test:1080", "socks5://telegram.test:1080"},
		{"socks5://shared.test:1080", "direct", "direct"},
	} {
		cfg, err := FromEnv(env(map[string]string{"SERVER_ADDR": "localhost", "BOT_TOKEN": "t", "ALLOWED_USER_IDS": "42", "PROXY_URL": tc.shared, "TELEGRAM_PROXY_URL": tc.telegram}))
		if err != nil || cfg.TelegramProxyURL != tc.want || cfg.ProxyURL != tc.shared {
			t.Fatalf("proxy precedence: cfg=%+v err=%v", cfg, err)
		}
	}
	for _, field := range []string{"PROXY_URL", "TELEGRAM_PROXY_URL"} {
		_, err := FromEnv(env(map[string]string{"SERVER_ADDR": "localhost", "BOT_TOKEN": "t", "ALLOWED_USER_IDS": "42", field: "ftp://user:SECRET@proxy.test"}))
		if err == nil || !strings.Contains(err.Error(), field) || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("invalid %s accepted or leaked: %v", field, err)
		}
	}
}
