package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVersionFlag(t *testing.T) {
	name := "memogram"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	flags := "-X github.com/usememos/memogram/internal/version.Version=26.10.0 -X github.com/usememos/memogram/internal/version.Commit=abcdef0123456789"
	if output, err := exec.Command("go", "build", "-buildvcs=false", "-ldflags", flags, "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	// Version inspection must work without configuring or contacting Telegram/Memos.
	t.Setenv("SERVER_ADDR", "")
	t.Setenv("BOT_TOKEN", "")
	output, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "memogram 26.10.0 (commit abcdef0123456789)" {
		t.Fatalf("--version: %v\n%s", err, output)
	}
	for _, access := range []string{"", "ALLOWED_USERNAMES=,", "ALLOWED_USER_IDS=invalid", "ALLOWED_USERNAMES=none\nALLOWED_USER_IDS=42"} {
		t.Run("startup/"+access, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			dir := t.TempDir()
			cmd := exec.CommandContext(ctx, binary)
			cmd.Dir = dir
			cmd.Env = []string{"SERVER_ADDR=http://127.0.0.1:1", "BOT_TOKEN=123:token", "DATA=" + filepath.Join(dir, "bindings.txt")}
			if access != "" {
				cmd.Env = append(cmd.Env, strings.Split(access, "\n")...)
			}
			output, err := cmd.CombinedOutput()
			if err == nil || ctx.Err() != nil || strings.Contains(string(output), "failed to start Memogram") || !strings.Contains(string(output), "invalid configuration") {
				t.Fatalf("invalid access reached networking or failed to stop: %v\n%s", err, output)
			}
			if _, err := os.Stat(filepath.Join(dir, "bindings.txt")); !os.IsNotExist(err) {
				t.Fatalf("invalid startup created binding storage: %v", err)
			}
		})
	}
}
