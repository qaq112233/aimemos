package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/bcrypt"

	"github.com/usememos/memogram/internal/config"
)

type savedSettings struct {
	PasswordHash []byte            `json:"password_hash"`
	AuthID       string            `json:"auth_id"`
	Notebook     *config.Notebook  `json:"notebook,omitempty"`
	APIKeys      map[string]string `json:"api_keys,omitempty"`
}

// Settings publishes ordinary immutable configuration values. It adds no
// reload queue, background watcher, or configuration versioning mechanism.
type Settings struct {
	mu      sync.Mutex
	path    string
	base    *config.Notebook
	saved   savedSettings
	current atomic.Pointer[config.Notebook]
}

func OpenSettings(path string, base *config.Notebook) (*Settings, error) {
	s := &Settings{path: path, base: base}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &s.saved); err != nil {
			return nil, fmt.Errorf("读取网页设置失败：%w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	notebook := s.saved.Notebook
	if notebook == nil {
		// Do not mutate the caller's startup configuration.
		data, err := json.Marshal(base)
		if err != nil {
			return nil, err
		}
		notebook = new(config.Notebook)
		if err := json.Unmarshal(data, notebook); err != nil {
			return nil, err
		}
	}
	s.applyRuntime(notebook, s.saved.APIKeys)
	if err := notebook.Validate(); err != nil {
		return nil, fmt.Errorf("网页设置无效：%w", err)
	}
	s.current.Store(notebook)
	return s, nil
}

func (s *Settings) Current() *config.Notebook { return s.current.Load() }

func (s *Settings) applyRuntime(c *config.Notebook, keys map[string]string) {
	c.Access, c.MemosToken, c.ProxyURL = s.base.Access, s.base.MemosToken, s.base.ProxyURL
	c.UserIDEnv, c.MemosTokenEnv = s.base.UserIDEnv, s.base.MemosTokenEnv
	for name, provider := range c.Providers {
		provider.APIKey = keys[name]
		c.Providers[name] = provider
	}
}

func (s *Settings) AuthID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved.AuthID
}

func (s *Settings) CheckPassword(password string) bool {
	s.mu.Lock()
	hash := append([]byte(nil), s.saved.PasswordHash...)
	s.mu.Unlock()
	return len(hash) > 0 && bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
}

func (s *Settings) SetPassword(oldPassword, password string, setup bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if setup && len(s.saved.PasswordHash) > 0 {
		return errors.New("密码已设置，请登录")
	}
	if !setup && bcrypt.CompareHashAndPassword(s.saved.PasswordHash, []byte(oldPassword)) != nil {
		return errors.New("原密码不正确")
	}
	if len(password) < 8 || len(password) > 72 {
		return errors.New("密码长度须为 8～72 字节")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	id := make([]byte, 24)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	next := s.saved
	next.PasswordHash, next.AuthID = hash, hex.EncodeToString(id)
	if err := s.write(next); err != nil {
		return err
	}
	s.saved = next
	return nil
}

type SettingsView struct {
	Notebook *config.Notebook `json:"notebook"`
	KeySet   map[string]bool  `json:"key_set"`
}

func (s *Settings) View() SettingsView {
	c := s.Current()
	status := make(map[string]bool, len(c.Providers))
	for name, provider := range c.Providers {
		status[name] = provider.APIKey != "" || os.Getenv(provider.KeyEnv) != ""
	}
	return SettingsView{Notebook: c, KeySet: status}
}

func (s *Settings) Save(c *config.Notebook, keys map[string]string, resetKeys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c == nil {
		return errors.New("缺少配置内容")
	}
	nextKeys := make(map[string]string)
	for name, value := range s.saved.APIKeys {
		if _, ok := c.Providers[name]; ok {
			nextKeys[name] = value
		}
	}
	for _, name := range resetKeys {
		delete(nextKeys, name)
	}
	for name, value := range keys {
		if _, ok := c.Providers[name]; !ok {
			return errors.New("密钥对应的供应商不存在")
		}
		if value != "" {
			nextKeys[name] = value
		}
	}
	s.applyRuntime(c, nextKeys)
	if err := c.Validate(); err != nil {
		return fmt.Errorf("配置校验失败：%w", err)
	}
	next := s.saved
	next.Notebook, next.APIKeys = c, nextKeys
	if err := s.write(next); err != nil {
		return err
	}
	s.saved = next
	s.current.Store(c)
	return nil
}

func (s *Settings) write(value savedSettings) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".web-settings-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}
