package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usememos/memogram/internal/config"
	"github.com/usememos/memogram/internal/testproxy"
	"github.com/usememos/memogram/internal/workflow"
)

func ptr[T any](v T) *T { return &v }

func TestProtocolsParametersImagesAndUsage(t *testing.T) {
	t.Setenv("TEST_AI_A", "key-a")
	t.Setenv("TEST_AI_B", "key-b")
	for _, protocol := range []string{"responses", "chat_completions"} {
		for _, vision := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/images=%t", protocol, vision), func(t *testing.T) {
				var received map[string]any
				serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer key-a" || r.Header.Get("X-Provider") != "a" {
						t.Errorf("bad provider A routing: %s", r.URL.Path)
					}
					if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"# note\n\n#Go"}]}],"usage":{"input_tokens":12,"output_tokens":23}}`)
				}))
				defer serverA.Close()
				serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer key-b" {
						t.Errorf("bad provider B routing: %s", r.URL.Path)
					}
					if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"id":"chat_1","choices":[{"message":{"role":"assistant","content":"# note\n\n#Go"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":23}}`)
				}))
				defer serverB.Close()
				provider := "a"
				if protocol == "chat_completions" {
					provider = "b"
				}
				m := config.Model{Provider: provider, ID: "configured-alias", Protocol: protocol, Vision: true, ReasoningBudgetField: "thinking.budget_tokens", Defaults: config.Parameters{MaxTokens: ptr(int64(100)), Temperature: ptr(0.2)}}
				if protocol == "chat_completions" {
					m.TokenField = "max_tokens"
				}
				cfg := &config.Notebook{Providers: map[string]config.Provider{
					"a": {BaseURL: serverA.URL + "/v1", KeyEnv: "TEST_AI_A", Timeout: time.Second, Headers: map[string]string{"X-Provider": "a"}},
					"b": {BaseURL: serverB.URL + "/v1", KeyEnv: "TEST_AI_B", Timeout: time.Second}}, Models: map[string]config.Model{"chosen": m}, SystemPrompt: "base prompt", TagPrompt: "tag prompt", CommonTags: []string{"Go"}}
				profile := config.Profile{Prompt: "profile prompt", Images: vision, Tags: true, Parameters: config.Parameters{MaxTokens: ptr(int64(200)), ReasoningEffort: ptr("medium"), ReasoningBudget: ptr(int64(50)), Verbosity: ptr("low"), Extra: map[string]any{"thinking": map[string]any{"enabled": true}}}}
				params := config.Parameters{MaxTokens: ptr(int64(300)), TopP: ptr(0.9), Extra: map[string]any{"temperature": 0.7}}
				fetches := 0
				client := New(cfg, func(context.Context, workflow.Attachment) (workflow.File, error) {
					fetches++
					return workflow.File{Data: []byte("\x89PNG\r\n\x1a\nfixture")}, nil
				})
				result, err := client.Generate(context.Background(), workflow.Request{Model: "chosen", Profile: profile, Parameters: params, Draft: "keep my edited draft", Requirements: []string{"remove old section"}, Materials: []workflow.Material{{Text: "caption https://source.test", Attachments: []workflow.Attachment{{ID: "file-id-not-a-url", Name: "photo.png", Image: true}}}}})
				if err != nil {
					t.Fatal(err)
				}
				if result.Text != "# note\n\n#Go" || result.Truncated || result.InputTokens != 12 || result.OutputTokens != 23 {
					t.Fatalf("bad result: %+v", result)
				}
				tokenField := "max_output_tokens"
				if protocol == "chat_completions" {
					tokenField = "max_tokens"
				}
				if received["model"] != "configured-alias" || received[tokenField] != float64(300) || received["temperature"] != 0.7 || received["top_p"] != 0.9 || received["store"] != false {
					t.Fatalf("bad parameters: %+v", received)
				}
				thinking := received["thinking"].(map[string]any)
				if thinking["budget_tokens"] != float64(50) || thinking["enabled"] != true {
					t.Fatalf("lost nested extension: %+v", thinking)
				}
				if protocol == "responses" {
					if received["reasoning"].(map[string]any)["effort"] != "medium" || received["text"].(map[string]any)["verbosity"] != "low" {
						t.Fatal("responses parameter mapping")
					}
				} else if received["reasoning_effort"] != "medium" || received["verbosity"] != "low" {
					t.Fatal("chat parameter mapping")
				}
				encoded, _ := json.Marshal(received)
				body := string(encoded)
				if strings.Contains(body, "file-id-not-a-url") || strings.Contains(body, "api.telegram.org") || !strings.Contains(body, "keep my edited draft") || !strings.Contains(body, "remove old section") {
					t.Fatalf("unsafe or missing input: %s", body)
				}
				if vision != (fetches == 1) || vision != strings.Contains(body, "data:image/png;base64,") {
					t.Fatalf("vision=%t fetches=%d body=%s", vision, fetches, body)
				}
				system, _ := received["instructions"].(string)
				if protocol == "chat_completions" {
					system = received["messages"].([]any)[0].(map[string]any)["content"].(string)
				}
				if vision {
					if !strings.Contains(system, "本次提供 1 张图片") || !strings.Contains(body, "materials[0].Attachments[0]") {
						t.Fatal("missing image count or attachment association")
					}
				} else if !strings.Contains(system, "本次没有提供图片像素") {
					t.Fatal("text-only request did not tell the model that pixels are absent")
				}
			})
		}
	}
}

func TestOptionalFieldsOmittedAndExtensionsWin(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		m := config.Model{Protocol: protocol}
		if body := parameters(m, config.Parameters{}); len(body) != 0 {
			t.Fatalf("sent unset optional values: %+v", body)
		}
		body := parameters(m, config.Parameters{Extra: map[string]any{"reasoning": map[string]any{"summary": "auto", "effort": "low"}}}, config.Parameters{Extra: map[string]any{"reasoning": map[string]any{"effort": "high"}}})
		if body["reasoning"].(map[string]any)["effort"] != "high" || body["reasoning"].(map[string]any)["summary"] != "auto" {
			t.Fatal("extension precedence")
		}
	}
}

func TestHigherLayerOrdinaryParametersOverrideLowerExtensions(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		m := config.Model{Protocol: protocol}
		lower := config.Parameters{Extra: map[string]any{"temperature": 0.8, "reasoning": map[string]any{"effort": "high", "summary": "auto"}, "reasoning_effort": "high"}}
		profile := config.Parameters{Temperature: ptr(0.3), ReasoningEffort: ptr("low")}
		temporary := config.Parameters{Temperature: ptr(0.1)}
		body := parameters(m, lower, profile, temporary)
		if body["temperature"] != 0.1 {
			t.Fatalf("lower extra overrode temporary: %+v", body)
		}
		if protocol == "responses" {
			r := body["reasoning"].(map[string]any)
			if r["effort"] != "low" || r["summary"] != "auto" {
				t.Fatalf("lower extra overrode profile or lost unrelated leaf: %+v", body)
			}
		} else if body["reasoning_effort"] != "low" {
			t.Fatalf("lower extra overrode chat profile: %+v", body)
		}
	}
}

func TestTruncationAndNoRetryOrFallback(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				if calls == 1 {
					if protocol == "responses" {
						fmt.Fprint(w, `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`)
					} else {
						fmt.Fprint(w, `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`)
					}
				} else {
					w.WriteHeader(http.StatusTooManyRequests)
					fmt.Fprint(w, `{"error":{"message":"SECRET-DO-NOT-LOG","type":"rate_limit"}}`)
				}
			}))
			defer server.Close()
			cfg := &config.Notebook{Providers: map[string]config.Provider{"a": {BaseURL: server.URL, Timeout: time.Second}}, Models: map[string]config.Model{"m": {Provider: "a", ID: "id", Protocol: protocol}}}
			client := New(cfg, nil)
			r := workflow.Request{Model: "m"}
			result, err := client.Generate(context.Background(), r)
			if err != nil || !result.Truncated || result.Text != "partial" {
				t.Fatalf("bad truncation: %+v %v", result, err)
			}
			_, err = client.Generate(context.Background(), r)
			if err == nil || strings.Contains(err.Error(), "SECRET") || calls != 2 {
				t.Fatalf("retried or leaked response: %v calls=%d", err, calls)
			}
		})
	}
}

func TestRequestParametersCannotEnableToolsOrServerState(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		for _, field := range []string{"tools", "previous_response_id", "conversation", "stream", "functions", "function_call"} {
			t.Run(protocol+"/"+field, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"status":"completed","output":[],"choices":[{"message":{"content":"draft"},"finish_reason":"stop"}]}`)
				}))
				defer server.Close()
				cfg := &config.Notebook{Providers: map[string]config.Provider{"p": {BaseURL: server.URL, Timeout: time.Second}}, Models: map[string]config.Model{"m": {Provider: "p", ID: "model", Protocol: protocol}}}
				_, err := New(cfg, nil).Generate(context.Background(), workflow.Request{Model: "m", Parameters: config.Parameters{Extra: map[string]any{field: "override"}}})
				if err == nil || calls != 0 {
					t.Fatalf("reserved temporary field reached provider: err=%v calls=%d", err, calls)
				}
			})
		}
	}
}

func TestProvidersDoNotInheritSDKEnvironmentHeaders(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "unrelated-key")
	t.Setenv("OPENAI_ADMIN_KEY", "unrelated-admin")
	t.Setenv("OPENAI_ORG_ID", "unrelated-org")
	t.Setenv("OPENAI_PROJECT_ID", "unrelated-project")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Unrelated-Secret: unrelated-secret")
	t.Setenv("PROVIDER_KEY", "provider-key")
	for _, protocol := range []string{"responses", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, header := range []string{"OpenAI-Organization", "OpenAI-Project", "X-Unrelated-Secret"} {
					if r.Header.Get(header) != "" {
						t.Errorf("inherited unrelated SDK header %s", header)
					}
				}
				if r.Header.Get("Authorization") != "Bearer provider-key" || r.Header.Get("X-Provider") != "configured" {
					t.Error("lost explicit provider settings")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"draft"}]}],"choices":[{"message":{"content":"draft"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			cfg := &config.Notebook{Providers: map[string]config.Provider{"p": {BaseURL: server.URL, KeyEnv: "PROVIDER_KEY", Timeout: time.Second, Headers: map[string]string{"X-Provider": "configured"}}}, Models: map[string]config.Model{"m": {Provider: "p", ID: "model", Protocol: protocol}}}
			result, err := New(cfg, nil).Generate(context.Background(), workflow.Request{Model: "m"})
			if err != nil || result.Text != "draft" {
				t.Fatalf("provider request failed: %+v %v", result, err)
			}
		})
	}
}

func TestProviderProxyRoutingAndIsolation(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			var mu sync.Mutex
			counts := map[string]int{}
			handler := func(route string) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					counts[route]++
					mu.Unlock()
					wantPath := "/v1/responses"
					if protocol == "chat_completions" {
						wantPath = "/v1/chat/completions"
					}
					if r.URL.Path != wantPath {
						t.Errorf("wrong proxy destination: %s", r.URL)
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"draft"}]}],"choices":[{"message":{"content":"draft"},"finish_reason":"stop"}]}`)
				}
			}
			shared := httptest.NewServer(handler("shared"))
			defer shared.Close()
			dedicated := httptest.NewServer(handler("dedicated"))
			defer dedicated.Close()
			backend := httptest.NewServer(handler("backend"))
			defer backend.Close()
			socks := testproxy.NewSOCKS5(t, strings.TrimPrefix(backend.URL, "http://"), false)
			cfg := &config.Notebook{ProxyURL: shared.URL, Providers: map[string]config.Provider{
				"shared":    {BaseURL: "http://provider.invalid/v1", Timeout: time.Second},
				"dedicated": {BaseURL: "http://provider.invalid/v1", ProxyURL: dedicated.URL, Timeout: time.Second},
				"socks":     {BaseURL: "http://socks-provider.invalid/v1", ProxyURL: socks.URL, Timeout: time.Second},
				"direct":    {BaseURL: backend.URL + "/v1", ProxyURL: "direct", Timeout: time.Second},
			}, Models: map[string]config.Model{}}
			for name := range cfg.Providers {
				cfg.Models[name] = config.Model{Provider: name, ID: "model", Protocol: protocol}
			}
			client := New(cfg, nil)
			defer func() {
				for _, httpClient := range client.clients {
					httpClient.CloseIdleConnections()
				}
			}()
			for _, name := range []string{"shared", "dedicated", "socks", "direct", "shared"} {
				result, err := client.Generate(context.Background(), workflow.Request{Model: name})
				if err != nil || result.Text != "draft" {
					t.Fatalf("%s proxy routing failed: %+v %v", name, result, err)
				}
			}
			targets, _, _ := socks.Snapshot()
			mu.Lock()
			defer mu.Unlock()
			if counts["shared"] != 2 || counts["dedicated"] != 1 || counts["backend"] != 2 || len(targets) != 1 || targets[0] != "socks-provider.invalid:80" || len(client.clients) != 4 {
				t.Fatalf("proxy leakage, incorrect routing or unused clients: counts=%v targets=%v", counts, targets)
			}
		})
	}
}
