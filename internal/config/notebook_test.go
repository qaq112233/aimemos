package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNotebookExampleAndSecretReferences(t *testing.T) {
	t.Setenv("TELEGRAM_USER_ID", "42")
	t.Setenv("MEMOS_TOKEN", "private-unit-token")
	cfg, err := LoadNotebook("../../notebook.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UserID != 42 || cfg.MemosToken != "private-unit-token" || len(cfg.Providers) != 2 {
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
	for _, field := range []string{"input", "messages", "model", "tools", "store", "previous_response_id", "conversation"} {
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
