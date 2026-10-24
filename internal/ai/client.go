// Package ai adapts the two configured protocols through the official Go SDK.
// It has no Telegram or Memos credentials and cannot save notes.
package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"

	"github.com/usememos/memogram/internal/config"
	"github.com/usememos/memogram/internal/workflow"
)

type Client struct {
	cfg   *config.Notebook
	fetch workflow.Fetch
}

func New(cfg *config.Notebook, fetch workflow.Fetch) *Client { return &Client{cfg: cfg, fetch: fetch} }

func (c *Client) Generate(ctx context.Context, req workflow.Request) (workflow.Result, error) {
	m, ok := c.cfg.Models[req.Model]
	if !ok {
		return workflow.Result{}, errors.New("未知模型")
	}
	p := c.cfg.Providers[m.Provider]
	layers := []config.Parameters{m.Defaults, req.Profile.Parameters, req.Parameters}
	budget := false
	for _, layer := range layers {
		budget = budget || layer.ReasoningBudget != nil
	}
	if budget && m.ReasoningBudgetField == "" {
		return workflow.Result{}, errors.New("本模型未配置 reasoning_budget_field，不能发送思考预算")
	}
	body := parameters(m, layers...)
	if regexp.MustCompile(`(?i)\((?:[0-9]+|none|auto|minimal|low|medium|high|xhigh)\)$`).MatchString(m.ID) {
		_, extraReasoning := body["reasoning"]
		_, extraThinking := body["thinking"]
		_, extraEffort := body["reasoning_effort"]
		if budget || extraReasoning || extraThinking || extraEffort {
			return workflow.Result{}, errors.New("模型名已带思考后缀，请移除后缀或请求体中的重复思考设置")
		}
	}
	key := os.Getenv(p.KeyEnv)
	if p.KeyEnv != "" && key == "" {
		return workflow.Result{}, fmt.Errorf("请设置供应商 %s 的 %s 环境变量", m.Provider, p.KeyEnv)
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	opts := []option.RequestOption{option.WithBaseURL(strings.TrimRight(p.BaseURL, "/") + "/"), option.WithAPIKey(key), option.WithMaxRetries(0), option.WithHTTPClient(&http.Client{Timeout: p.Timeout})}
	for name, value := range p.Headers {
		opts = append(opts, option.WithHeader(name, value))
	}
	client := openai.NewClient(opts...)
	system := c.cfg.SystemPrompt + "\n\n" + req.Profile.Prompt
	if req.Profile.Tags {
		system += "\n\n" + c.cfg.TagPrompt + "\n常用标签：" + strings.Join(c.cfg.CommonTags, ", ")
	}
	// Do not include Telegram file IDs or download URLs in model inputs.
	type source struct {
		Number      int
		Text        string
		Sources     []string
		Attachments []string
	}
	var materials []source
	for i, material := range req.Materials {
		item := source{Number: i + 1, Text: material.Text, Sources: material.Sources}
		for _, a := range material.Attachments {
			name := a.Name
			if name == "" {
				name = fmt.Sprintf("素材 %d 的附件", i+1)
			}
			item.Attachments = append(item.Attachments, name)
		}
		materials = append(materials, item)
	}
	input, err := json.Marshal(struct {
		Materials    []source `json:"materials"`
		Draft        string   `json:"current_draft"`
		Requirements []string `json:"user_requirements_in_order"`
	}{materials, req.Draft, req.Requirements})
	if err != nil {
		return workflow.Result{}, errors.New("无法构造素材输入")
	}
	type imageInput struct {
		URL      string
		Material int
	}
	var images []imageInput
	if req.Profile.Images {
		if !m.Vision {
			return workflow.Result{}, errors.New("模型未声明图片能力，请换模型或关闭读图")
		}
		for i, material := range req.Materials {
			for _, a := range material.Attachments {
				if a.Image {
					if c.fetch == nil {
						return workflow.Result{}, errors.New("图片下载未配置")
					}
					file, err := c.fetch(ctx, a)
					if err != nil {
						return workflow.Result{}, errors.New("图片下载失败，素材保留，请重试或关闭读图")
					}
					mime := http.DetectContentType(file.Data)
					switch mime {
					case "image/jpeg", "image/png", "image/gif", "image/webp":
					default:
						return workflow.Result{}, errors.New("图片格式不受支持，请转换为 PNG/JPEG/WebP/GIF 或关闭读图")
					}
					images = append(images, imageInput{URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(file.Data), Material: i + 1})
				}
			}
		}
	}
	body["model"], body["store"] = m.ID, false
	if m.Protocol == "responses" {
		parts := []map[string]any{{"type": "input_text", "text": string(input)}}
		for _, image := range images {
			parts = append(parts, map[string]any{"type": "input_text", "text": fmt.Sprintf("materials[%d] image attachment", image.Material-1)}, map[string]any{"type": "input_image", "image_url": image.URL})
		}
		body["instructions"] = system
		body["input"] = []map[string]any{{"role": "user", "content": parts}}
		result, err := client.Responses.New(ctx, param.Override[responses.ResponseNewParams](body))
		if err != nil {
			return workflow.Result{}, safeError(err)
		}
		return workflow.Result{Text: result.OutputText(), Truncated: result.Status != "completed", InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens}, nil
	}
	parts := []map[string]any{{"type": "text", "text": string(input)}}
	for _, image := range images {
		parts = append(parts, map[string]any{"type": "text", "text": fmt.Sprintf("materials[%d] image attachment", image.Material-1)}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image.URL}})
	}
	body["messages"] = []map[string]any{{"role": "system", "content": system}, {"role": "user", "content": parts}}
	result, err := client.Chat.Completions.New(ctx, param.Override[openai.ChatCompletionNewParams](body))
	if err != nil {
		return workflow.Result{}, safeError(err)
	}
	if len(result.Choices) == 0 {
		return workflow.Result{}, errors.New("AI 返回空结果")
	}
	choice := result.Choices[0]
	return workflow.Result{Text: choice.Message.Content, Truncated: choice.FinishReason != "stop", InputTokens: result.Usage.PromptTokens, OutputTokens: result.Usage.CompletionTokens}, nil
}

func parameters(m config.Model, layers ...config.Parameters) map[string]any {
	body := map[string]any{}
	for _, p := range layers {
		layer := map[string]any{}
		if p.MaxTokens != nil {
			field := "max_output_tokens"
			if m.Protocol == "chat_completions" {
				field = m.TokenField
				if field == "" {
					field = "max_completion_tokens"
				}
			}
			layer[field] = *p.MaxTokens
		}
		if p.ReasoningEffort != nil {
			if m.Protocol == "responses" {
				layer["reasoning"] = map[string]any{"effort": *p.ReasoningEffort}
			} else {
				layer["reasoning_effort"] = *p.ReasoningEffort
			}
		}
		if p.ReasoningBudget != nil && m.ReasoningBudgetField != "" {
			setPath(layer, m.ReasoningBudgetField, *p.ReasoningBudget)
		}
		if p.Temperature != nil {
			layer["temperature"] = *p.Temperature
		}
		if p.TopP != nil {
			layer["top_p"] = *p.TopP
		}
		if p.Verbosity != nil {
			if m.Protocol == "responses" {
				layer["text"] = map[string]any{"verbosity": *p.Verbosity}
			} else {
				layer["verbosity"] = *p.Verbosity
			}
		}
		config.MergeMap(layer, p.Extra)
		config.MergeMap(body, layer)
	}
	return body
}

func setPath(body map[string]any, path string, value any) {
	keys := strings.Split(path, ".")
	for _, key := range keys[:len(keys)-1] {
		next, _ := body[key].(map[string]any)
		if next == nil {
			next = map[string]any{}
			body[key] = next
		}
		body = next
	}
	body[keys[len(keys)-1]] = value
}

func safeError(err error) error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return fmt.Errorf("AI 服务返回 HTTP %d，请检查模型、协议及参数配置", apiErr.StatusCode)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("AI 请求超时，请稍后重试")
	}
	return errors.New("AI 请求失败，请检查服务地址和网络")
}
