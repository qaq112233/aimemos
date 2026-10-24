package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Notebook is fixed configuration. Secrets are resolved only from environment
// references; session contents and model selections never go into this file.
type Notebook struct {
	UserIDEnv     string              `yaml:"user_id_env"`
	MemosTokenEnv string              `yaml:"memos_token_env"`
	StatusEvery   time.Duration       `yaml:"status_every"`
	MaxMaterials  int                 `yaml:"max_materials"`
	MaxTextBytes  int                 `yaml:"max_text_bytes"`
	MaxImages     int                 `yaml:"max_images"`
	Providers     map[string]Provider `yaml:"providers"`
	Models        map[string]Model    `yaml:"models"`
	Profiles      map[string]Profile  `yaml:"profiles"`
	Entries       map[string]Entry    `yaml:"entries"`
	SystemPrompt  string              `yaml:"system_prompt"`
	TagPrompt     string              `yaml:"tag_prompt"`
	CommonTags    []string            `yaml:"common_tags"`
	UserID        int64               `yaml:"-"`
	MemosToken    string              `yaml:"-"`
}

type Provider struct {
	BaseURL string            `yaml:"base_url"`
	KeyEnv  string            `yaml:"key_env"`
	Timeout time.Duration     `yaml:"timeout"`
	Headers map[string]string `yaml:"headers"`
}

type Model struct {
	Provider             string     `yaml:"provider"`
	ID                   string     `yaml:"id"`
	Protocol             string     `yaml:"protocol"`
	Vision               bool       `yaml:"vision"`
	TokenField           string     `yaml:"token_field"`
	ReasoningBudgetField string     `yaml:"reasoning_budget_field"`
	Defaults             Parameters `yaml:"parameters"`
}

type Profile struct {
	Name       string     `yaml:"name"`
	Prompt     string     `yaml:"prompt"`
	Model      string     `yaml:"model"`
	Images     bool       `yaml:"images"`
	Tags       bool       `yaml:"tags"`
	Parameters Parameters `yaml:"parameters"`
}

type Entry struct {
	Label   string `yaml:"label"`
	Action  string `yaml:"action"` // raw or generate
	Profile string `yaml:"profile"`
	Custom  bool   `yaml:"custom"` // requires "entry prompt", never prefix matching
}

type Parameters struct {
	MaxTokens       *int64         `yaml:"max_tokens"`
	ReasoningEffort *string        `yaml:"reasoning_effort"`
	ReasoningBudget *int64         `yaml:"reasoning_budget"`
	Temperature     *float64       `yaml:"temperature"`
	TopP            *float64       `yaml:"top_p"`
	Verbosity       *string        `yaml:"verbosity"`
	Extra           map[string]any `yaml:"extra"`
}

// Merge applies model < profile < temporary. Extra maps merge recursively;
// explicit protocol extension leaves take precedence over ordinary parameters.
func Merge(layers ...Parameters) Parameters {
	var out Parameters
	out.Extra = map[string]any{}
	for _, p := range layers {
		if p.MaxTokens != nil {
			out.MaxTokens = p.MaxTokens
		}
		if p.ReasoningEffort != nil {
			out.ReasoningEffort = p.ReasoningEffort
		}
		if p.ReasoningBudget != nil {
			out.ReasoningBudget = p.ReasoningBudget
		}
		if p.Temperature != nil {
			out.Temperature = p.Temperature
		}
		if p.TopP != nil {
			out.TopP = p.TopP
		}
		if p.Verbosity != nil {
			out.Verbosity = p.Verbosity
		}
		MergeMap(out.Extra, p.Extra)
	}
	return out
}

func MergeMap(dst, src map[string]any) {
	for k, v := range src {
		if nested, ok := v.(map[string]any); ok {
			child, _ := dst[k].(map[string]any)
			if child == nil {
				child = map[string]any{}
			}
			MergeMap(child, nested)
			dst[k] = child
		} else {
			dst[k] = v
		}
	}
}

// ReadNotebook validates fixed configuration without requiring credentials or
// making network requests, so examples can be checked on a fresh checkout.
func ReadNotebook(path string) (*Notebook, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c Notebook
	d := yaml.NewDecoder(f)
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("expected one YAML document")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func LoadNotebook(path string) (*Notebook, error) {
	c, err := ReadNotebook(path)
	if err != nil {
		return nil, err
	}
	c.UserID, err = strconv.ParseInt(os.Getenv(c.UserIDEnv), 10, 64)
	if err != nil || c.UserID <= 0 {
		return nil, fmt.Errorf("set %s to your numeric Telegram user ID", c.UserIDEnv)
	}
	c.MemosToken = strings.TrimSpace(os.Getenv(c.MemosTokenEnv))
	if c.MemosToken == "" {
		return nil, fmt.Errorf("%s is required", c.MemosTokenEnv)
	}
	return c, nil
}

func (c *Notebook) Validate() error {
	if c.UserIDEnv == "" || c.MemosTokenEnv == "" {
		return errors.New("user_id_env and memos_token_env are required")
	}
	if c.StatusEvery <= 0 || c.MaxMaterials <= 0 || c.MaxTextBytes <= 0 || c.MaxImages <= 0 {
		return errors.New("status_every and material limits must be positive")
	}
	if c.SystemPrompt == "" || c.TagPrompt == "" {
		return errors.New("system_prompt and tag_prompt are required")
	}
	for name, p := range c.Providers {
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || p.Timeout <= 0 {
			return fmt.Errorf("invalid provider %s URL or timeout", name)
		}
		for h := range p.Headers {
			if strings.EqualFold(h, "Authorization") || strings.EqualFold(h, "Host") {
				return fmt.Errorf("provider %s: use key_env for authentication", name)
			}
		}
	}
	for name, m := range c.Models {
		if _, ok := c.Providers[m.Provider]; !ok || m.ID == "" {
			return fmt.Errorf("model %s: unknown provider or empty ID", name)
		}
		if m.Protocol != "responses" && m.Protocol != "chat_completions" {
			return fmt.Errorf("model %s: select responses or chat_completions", name)
		}
		if m.TokenField != "" && (m.Protocol != "chat_completions" || (m.TokenField != "max_tokens" && m.TokenField != "max_completion_tokens")) {
			return fmt.Errorf("model %s: invalid token_field", name)
		}
		if field := m.ReasoningBudgetField; field != "" {
			if !regexp.MustCompile(`^[a-z_][a-z_0-9]*(\.[a-z_][a-z_0-9]*)*$`).MatchString(field) {
				return fmt.Errorf("model %s: invalid reasoning_budget_field", name)
			}
			if err := validateParameters(Parameters{Extra: map[string]any{strings.Split(field, ".")[0]: true}}); err != nil {
				return fmt.Errorf("model %s: %w", name, err)
			}
		}
		if err := validateParameters(m.Defaults); err != nil {
			return fmt.Errorf("model %s: %w", name, err)
		}
	}
	for name, p := range c.Profiles {
		if _, ok := c.Models[p.Model]; !ok || p.Prompt == "" {
			return fmt.Errorf("profile %s: unknown model or empty prompt", name)
		}
		if err := validateParameters(p.Parameters); err != nil {
			return fmt.Errorf("profile %s: %w", name, err)
		}
	}
	if len(c.Entries) == 0 {
		return errors.New("entries are required")
	}
	for key, e := range c.Entries {
		if key == "" || strings.ContainsAny(key, " \t\r\n:") || len(key) > 12 {
			return fmt.Errorf("invalid entry %q", key)
		}
		if e.Action == "raw" && !e.Custom {
			continue
		}
		if _, ok := c.Profiles[e.Profile]; !ok || e.Action != "generate" {
			return fmt.Errorf("entry %s: invalid action or profile", key)
		}
	}
	return nil
}

func validateParameters(p Parameters) error {
	if p.MaxTokens != nil && *p.MaxTokens <= 0 {
		return errors.New("max_tokens must be positive")
	}
	// Extensions are trusted configuration, but cannot replace inputs, routing,
	// authentication, tool calls, or server-side conversation state.
	for k := range p.Extra {
		switch k {
		case "model", "messages", "input", "instructions", "prompt", "tools", "tool_choice", "stream", "store", "previous_response_id", "conversation", "background", "service_tier", "headers", "extra_headers", "authorization", "api_key", "base_url":
			return fmt.Errorf("extra.%s is reserved", k)
		}
	}
	return nil
}
