// Package config reads Memogram's settings from the environment.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"

	"github.com/usememos/memogram/internal/memos"
	"github.com/usememos/memogram/internal/netproxy"
)

// DefaultDataPath is used when DATA is unset. Docker images set DATA to a path
// inside their persistent volume.
const DefaultDataPath = "data.txt"

type Config struct {
	// ServerAddr is the normalized Memos base URL (SERVER_ADDR).
	ServerAddr string
	// Optional Cloudflare Access service token for the Memos endpoint.
	MemosCFClientID     string
	MemosCFClientSecret string
	// BotToken is the Telegram bot token (BOT_TOKEN).
	BotToken string
	// BotProxyAddr optionally replaces the Telegram Bot API server (BOT_PROXY_ADDR).
	BotProxyAddr string
	// ProxyURL is the shared proxy for Telegram and AI providers (PROXY_URL).
	ProxyURL string
	// TelegramProxyURL overrides the shared proxy (TELEGRAM_PROXY_URL).
	TelegramProxyURL string
	// DataPath is the file that stores user bindings (DATA).
	DataPath    string
	Access      Access
	Notebook    *Notebook
	WebAddr     string
	WebURL      string
	WebDataPath string
}

// Load reads the configuration from the environment, after loading a .env
// file from the working directory if one exists. Variables already set in the
// environment take precedence over .env.
func Load() (*Config, error) {
	const envFile = ".env"
	if err := godotenv.Load(envFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load %s: %w", envFile, err)
	}
	return FromEnv(os.Getenv)
}

// FromEnv builds and validates a configuration from the given lookup function.
func FromEnv(getenv func(string) string) (*Config, error) {
	cfg := &Config{
		MemosCFClientID:     strings.TrimSpace(getenv("MEMOS_CF_ACCESS_CLIENT_ID")),
		MemosCFClientSecret: strings.TrimSpace(getenv("MEMOS_CF_ACCESS_CLIENT_SECRET")),
		BotToken:            strings.TrimSpace(getenv("BOT_TOKEN")),
		BotProxyAddr:        strings.TrimSpace(getenv("BOT_PROXY_ADDR")),
		ProxyURL:            strings.TrimSpace(getenv("PROXY_URL")),
		TelegramProxyURL:    strings.TrimSpace(getenv("TELEGRAM_PROXY_URL")),
		DataPath:            strings.TrimSpace(getenv("DATA")),
	}
	serverAddr := getenv("SERVER_ADDR")
	if strings.TrimSpace(serverAddr) == "" {
		return nil, errors.New("SERVER_ADDR is required")
	}
	var err error
	if cfg.ServerAddr, err = memos.NormalizeBaseURL(serverAddr); err != nil {
		return nil, fmt.Errorf("SERVER_ADDR %w", err)
	}
	if (cfg.MemosCFClientID == "") != (cfg.MemosCFClientSecret == "") {
		return nil, errors.New("MEMOS_CF_ACCESS_CLIENT_ID and MEMOS_CF_ACCESS_CLIENT_SECRET must be set together")
	}
	if cfg.BotToken == "" {
		return nil, errors.New("BOT_TOKEN is required")
	}
	if _, err := netproxy.Parse(cfg.ProxyURL); err != nil {
		return nil, fmt.Errorf("PROXY_URL: %w", err)
	}
	if cfg.TelegramProxyURL == "" {
		cfg.TelegramProxyURL = cfg.ProxyURL
	}
	if _, err := netproxy.Parse(cfg.TelegramProxyURL); err != nil {
		return nil, fmt.Errorf("TELEGRAM_PROXY_URL: %w", err)
	}
	if cfg.DataPath == "" {
		cfg.DataPath = DefaultDataPath
	}
	if path := strings.TrimSpace(getenv("NOTEBOOK_CONFIG")); path != "" {
		cfg.Notebook, err = loadNotebook(path, getenv)
		if err != nil {
			return nil, fmt.Errorf("NOTEBOOK_CONFIG: %w", err)
		}
		cfg.Access = cfg.Notebook.Access
		cfg.WebAddr = strings.TrimSpace(getenv("WEB_ADDR"))
		if cfg.WebAddr == "" {
			cfg.WebAddr = "127.0.0.1:8080"
		}
		if cfg.WebAddr == "off" {
			cfg.WebAddr = ""
		}
		cfg.WebDataPath = filepath.Join(filepath.Dir(cfg.DataPath), "web-settings.json")
		cfg.WebURL = strings.TrimSpace(getenv("WEB_URL"))
		if cfg.WebURL != "" {
			u, err := url.Parse(cfg.WebURL)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return nil, errors.New("WEB_URL 必须是公网 HTTPS 地址")
			}
		}
	} else {
		cfg.Access, err = readAccess(getenv, "")
		if err != nil {
			return nil, err
		}
	}
	return cfg, nil
}
