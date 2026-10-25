package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotebookExampleAndSecretReferences(t *testing.T) {
	t.Setenv("ALLOWED_USERNAMES", "")
	t.Setenv("ALLOWED_USER_IDS", "")
	t.Setenv("TELEGRAM_USER_ID", "42")
	t.Setenv("MEMOS_TOKEN", "private-unit-token")
	cfg, err := LoadNotebook("../../notebook.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Access.Allows(42, "") || cfg.MemosToken != "private-unit-token" || len(cfg.Providers) != 2 {
		t.Fatal("example configuration did not resolve references")
	}
	t.Setenv("TELEGRAM_USER_ID", "")
	if _, err := LoadNotebook("../../notebook.example.yaml"); err == nil {
		t.Fatal("accepted missing access restriction")
	}
	if _, err := ReadNotebook("../../notebook.example.yaml"); err != nil {
		t.Fatal("static validation requires credentials")
	}
	data, err := os.ReadFile("../../notebook.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, append(data, []byte("\nunknown_setting: true\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadNotebook(path); err == nil {
		t.Fatal("ignored unknown configuration key")
	}
}

func TestNotebookRejectsInputOverridesAndInvalidProtocols(t *testing.T) {
	for _, field := range []string{"input", "messages", "model", "tools", "functions", "function_call", "store", "previous_response_id", "conversation"} {
		cfg, err := ReadNotebook("../../notebook.example.yaml")
		if err != nil {
			t.Fatal(err)
		}
		p := cfg.Profiles["tidy"]
		p.Parameters.Extra = map[string]any{field: "override"}
		cfg.Profiles["tidy"] = p
		if err := cfg.Validate(); err == nil {
			t.Errorf("accepted reserved field %s", field)
		}
	}
	cfg, err := ReadNotebook("../../notebook.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Models["primary"]
	m.Protocol = "automatic"
	cfg.Models["primary"] = m
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted implicit protocol routing")
	}
}

func TestProviderProxyOverridesAndSecretReference(t *testing.T) {
	t.Setenv("UNIT_PROVIDER_PROXY", "socks5://user:pass@provider.test:1080")
	for _, tc := range []struct {
		provider Provider
		want     string
	}{
		{Provider{}, "http://shared.test:8080"},
		{Provider{ProxyURL: "direct"}, "direct"},
		{Provider{ProxyURL: "http://provider.test:8080"}, "http://provider.test:8080"},
		{Provider{ProxyEnv: "UNIT_PROVIDER_PROXY"}, "socks5://user:pass@provider.test:1080"},
	} {
		got, err := tc.provider.NetworkProxy("http://shared.test:8080")
		if err != nil || got != tc.want {
			t.Fatalf("wrong provider proxy: got=%q err=%v", got, err)
		}
	}
	t.Setenv("UNIT_MISSING_PROXY", "")
	if _, err := (Provider{ProxyEnv: "UNIT_MISSING_PROXY"}).NetworkProxy("http://shared.test:8080"); err == nil {
		t.Fatal("missing explicit proxy silently fell back to shared proxy")
	}
	t.Setenv("UNIT_BAD_PROXY", "ftp://user:SECRET@proxy.test")
	if _, err := (Provider{ProxyEnv: "UNIT_BAD_PROXY"}).NetworkProxy(""); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("bad proxy accepted or leaked: %v", err)
	}
	for _, provider := range []Provider{{ProxyURL: "http://proxy.test", ProxyEnv: "UNIT_PROVIDER_PROXY"}, {ProxyURL: "ftp://proxy.test"}} {
		cfg, err := ReadNotebook("../../notebook.example.yaml")
		if err != nil {
			t.Fatal(err)
		}
		original := cfg.Providers["cli_proxy"]
		original.ProxyURL, original.ProxyEnv = provider.ProxyURL, provider.ProxyEnv
		cfg.Providers["cli_proxy"] = original
		if err := cfg.Validate(); err == nil {
			t.Fatal("invalid provider proxy passed static YAML validation")
		}
	}
}
