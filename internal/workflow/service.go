// Package workflow owns the in-memory notebook. It has no Telegram types.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/usememos/memogram/internal/config"
)

type Attachment struct {
	ID, Name, MIME string
	Image          bool
}
type File struct {
	Name, MIME string
	Data       []byte
}
type Material struct {
	ID          int
	Text        string
	Sources     []string
	Attachments []Attachment
}
type Request struct {
	Model        string
	Profile      config.Profile
	Materials    []Material
	Draft        string
	Requirements []string
	Parameters   config.Parameters // Temporary overrides; AI applies model < profile < temporary.
}
type Result struct {
	Text                      string
	Truncated                 bool
	InputTokens, OutputTokens int64
}
type Generator interface {
	Generate(context.Context, Request) (Result, error)
}
type Memo struct{ Name, URL string }
type Destination interface {
	Create(context.Context, string) (Memo, error)
	Upload(context.Context, Memo, Attachment) error
}
type Fetch func(context.Context, Attachment) (File, error)

type Draft struct {
	Version   int
	Text      string
	Count     int
	Truncated bool
}
type View struct {
	Group                                   string
	Count, Images, Files                    int
	Collecting, Generating, Saving, Partial bool
	Draft                                   Draft
	Model                                   string
	Pending                                 int
	Memo                                    Memo
	Notice                                  string
}
type group struct {
	id                             string
	materials                      []Material
	draft                          Draft
	collecting, generating, saving bool
	model, profile                 string
	requirements                   []string
	memo                           Memo
	pending                        []Attachment
	saveCount                      int
}
type Service struct {
	mu     sync.Mutex
	cfg    config.NotebookSource
	gen    Generator
	dst    Destination
	groups map[string]*group
}

func New(cfg config.NotebookSource, gen Generator, dst Destination) *Service {
	return &Service{cfg: cfg, gen: gen, dst: dst, groups: make(map[string]*group)}
}

func (s *Service) view(key, notice string) View {
	cfg := s.cfg.Current()
	v := View{Notice: notice}
	if g := s.groups[key]; g != nil {
		v.Group, v.Count, v.Draft, v.Model = g.id, len(g.materials), g.draft, g.model
		if v.Model == "" && g.profile != "" {
			v.Model = cfg.Profiles[g.profile].Model
		}
		v.Collecting, v.Generating, v.Saving = g.collecting, g.generating, g.saving
		v.Partial, v.Memo = g.memo.Name != "", g.memo
		if g.draft.Version > 0 {
			v.Pending = len(g.materials) - g.draft.Count
		}
		for _, m := range g.materials {
			for _, a := range m.Attachments {
				if a.Image {
					v.Images++
				} else {
					v.Files++
				}
			}
		}
	}
	return v
}

func (s *Service) check(key, id string) error {
	if s.groups[key] == nil || (id != "" && id != s.groups[key].id) {
		return errors.New("本组已结束或按钮已过期，请发送素材或使用最新按钮")
	}
	return nil
}

func (s *Service) Status(key string) View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view(key, "")
}

func (s *Service) Add(key string, m Material) (View, error) {
	cfg := s.cfg.Current()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.groups[key] == nil {
		if len(m.Text) > cfg.MaxTextBytes {
			return s.view(key, ""), errors.New("素材已达配置上限，请先缩短素材")
		}
		s.groups[key] = &group{id: uuid.NewString()[:12], collecting: true}
	}
	if err := s.check(key, ""); err != nil {
		return View{}, err
	}
	g := s.groups[key]
	if g.saving || g.memo.Name != "" {
		return s.view(key, ""), errors.New("正在保存或等待补传附件，请先完成保存后再发送新素材")
	}
	for _, old := range g.materials {
		if m.ID != 0 && old.ID == m.ID {
			return s.view(key, ""), nil
		}
	}
	total := len(m.Text)
	for _, old := range g.materials {
		total += len(old.Text)
	}
	if len(g.materials) >= cfg.MaxMaterials || total > cfg.MaxTextBytes {
		return s.view(key, ""), errors.New("素材已达配置上限，请先处理本组")
	}
	m.Sources, m.Attachments = slices.Clone(m.Sources), slices.Clone(m.Attachments)
	g.materials = append(g.materials, m)
	notice := "已收集"
	if g.generating || g.draft.Version > 0 {
		notice = "新增素材尚未纳入草稿，请重新整理"
	}
	return s.view(key, notice), nil
}

func (s *Service) Collect(key, id string) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(key, id); err != nil {
		return View{}, err
	}
	if s.groups[key].saving || s.groups[key].memo.Name != "" {
		return s.view(key, ""), errors.New("请先完成保存或补传")
	}
	s.groups[key].collecting = true
	return s.view(key, "已进入补充素材状态，已有草稿保留"), nil
}

func (s *Service) SelectModel(key, model string) (View, error) {
	cfg := s.cfg.Current()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := cfg.Models[model]; !ok {
		return s.view(key, ""), errors.New("未知模型，请使用 /model 查看配置名称")
	}
	if err := s.check(key, ""); err != nil {
		return View{}, err
	}
	if s.groups[key].generating || s.groups[key].saving || s.groups[key].memo.Name != "" {
		return s.view(key, ""), errors.New("请等待当前操作完成后再切换模型")
	}
	s.groups[key].model = model
	return s.view(key, "模型已切换，下一次生成生效；已有草稿和修改要求保留"), nil
}

func (s *Service) Discard(key, id string) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(key, id); err != nil {
		return View{}, err
	}
	if s.groups[key].generating || s.groups[key].saving {
		return s.view(key, ""), errors.New("当前操作进行中，请完成后再放弃")
	}
	notice := "已放弃并清空本组"
	if s.groups[key].memo.Name != "" {
		notice += "；已创建的 Memos 笔记保留"
	}
	delete(s.groups, key)
	return s.view(key, notice), nil
}

// BeginGenerate takes the snapshot before returning. The caller runs the work
// separately, leaving Add available while network requests are in flight.
func (s *Service) BeginGenerate(key, id, profile, instruction string, temporary config.Parameters) (View, func(context.Context) View, error) {
	cfg := s.cfg.Current()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(key, id); err != nil {
		return View{}, nil, err
	}
	g := s.groups[key]
	if g.generating || g.saving || g.memo.Name != "" {
		return s.view(key, ""), nil, errors.New("当前操作尚未完成，请稍后再试")
	}
	if s.gen == nil {
		return s.view(key, ""), nil, errors.New("AI 未配置；素材保留，可以原样保存")
	}
	if profile == "" {
		profile = g.profile
	}
	p, ok := cfg.Profiles[profile]
	if !ok {
		return s.view(key, ""), nil, errors.New("请先选择整理方案")
	}
	model := g.model
	if model == "" {
		model = p.Model
	}
	m := cfg.Models[model]
	if p.Images && !m.Vision {
		return s.view(key, ""), nil, errors.New("此模型不支持图片，请切换模型或选择关闭读图的方案")
	}
	images := 0
	for _, material := range g.materials {
		for _, a := range material.Attachments {
			if a.Image {
				images++
			}
		}
	}
	if p.Images && images > cfg.MaxImages {
		return s.view(key, ""), nil, errors.New("图片数量超过 max_images，请选择关闭读图的方案或调整配置")
	}
	if instruction != "" {
		g.requirements = append(g.requirements, instruction)
	}
	g.profile, g.generating = profile, true
	req := Request{Model: model, Profile: p, Materials: slices.Clone(g.materials), Draft: g.draft.Text, Requirements: slices.Clone(g.requirements), Parameters: temporary}
	return s.view(key, "正在生成素材快照；期间仍可发送新素材"), func(ctx context.Context) View {
		result, err := s.gen.Generate(ctx, req)
		s.mu.Lock()
		defer s.mu.Unlock()
		g.generating = false
		if err != nil {
			return s.view(key, "AI 生成失败，素材、已有草稿和修改要求保留。"+err.Error())
		}
		if strings.TrimSpace(result.Text) == "" {
			return s.view(key, "AI 返回空内容，已有草稿保留，请重试")
		}
		g.draft = Draft{Version: g.draft.Version + 1, Text: result.Text, Count: len(req.Materials), Truncated: result.Truncated}
		g.collecting = false
		notice := "草稿已更新，请检查后确认保存"
		if result.Truncated {
			notice = "输出因上限或服务状态中断，草稿不完整，不能确认保存；请调整输出上限后重新整理"
		}
		if len(g.materials) > g.draft.Count {
			notice += "\n新增素材尚未纳入，本草稿仅包含本次快照"
		}
		if result.InputTokens+result.OutputTokens > 0 {
			notice += fmt.Sprintf("\n用量：输入 %d / 输出 %d Token（输出可能含思考）", result.InputTokens, result.OutputTokens)
		}
		return s.view(key, notice)
	}, nil
}

// BeginSave freezes either the raw group or the current, exact draft version.
// A partial save retains the memo identity and only the failed attachments.
func (s *Service) BeginSave(key, id string, version int, raw bool) (View, func(context.Context) View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(key, id); err != nil {
		return View{}, nil, err
	}
	g := s.groups[key]
	if g.generating || g.saving {
		return s.view(key, ""), nil, errors.New("当前操作进行中，请勿重复保存")
	}
	if g.memo.Name == "" && !raw && (g.draft.Version == 0 || g.draft.Version != version || g.draft.Truncated) {
		return s.view(key, ""), nil, errors.New("此草稿已过期或不完整，请使用最新完整草稿的确认按钮")
	}
	content, count := g.draft.Text, g.draft.Count
	if raw {
		content, count = WithSources(Raw(g.materials), g.materials), len(g.materials)
	} else {
		content = WithSources(content, g.materials[:count])
	}
	if g.memo.Name == "" {
		g.saveCount = count
		g.pending = nil
		for _, m := range g.materials[:count] {
			g.pending = append(g.pending, m.Attachments...)
		}
	} else {
		count = g.saveCount
	}
	g.saving = true
	return s.view(key, "正在保存"), func(ctx context.Context) View {
		memo := g.memo
		if memo.Name == "" {
			var err error
			memo, err = s.dst.Create(ctx, content)
			if err != nil {
				s.mu.Lock()
				defer s.mu.Unlock()
				g.saving = false
				return s.view(key, "Memos 正文保存失败，内容保留，请重试。"+err.Error())
			}
		}
		var failed []Attachment
		for _, a := range g.pending {
			if err := s.dst.Upload(ctx, memo, a); err != nil {
				failed = append(failed, a)
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		g.memo, g.pending, g.saving = memo, failed, false
		if len(failed) > 0 {
			return s.view(key, fmt.Sprintf("正文已保存，%d 个附件尚未上传，请点击重试补传。\n%s", len(failed), memo.URL))
		}
		// New material received during generation was not part of this draft.
		// Saving that exact version keeps those materials as the next group.
		remaining := slices.Clone(g.materials[count:])
		delete(s.groups, key)
		notice := "保存成功\n" + memo.URL
		if len(remaining) > 0 {
			s.groups[key] = &group{id: uuid.NewString()[:12], materials: remaining, collecting: true}
			notice += "\n未纳入的新增素材已保留为下一组"
		}
		return s.view(key, notice)
	}, nil
}

func Raw(materials []Material) string {
	parts := make([]string, 0, len(materials))
	for _, m := range materials {
		if m.Text != "" {
			parts = append(parts, m.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func WithSources(text string, materials []Material) string {
	seen := map[string]bool{}
	var sources []string
	for _, m := range materials {
		for _, source := range m.Sources {
			if !seen[source] && !strings.Contains(text, source) {
				sources = append(sources, "- "+source)
				seen[source] = true
			}
		}
	}
	if len(sources) > 0 {
		text += "\n\n来源：\n" + strings.Join(sources, "\n")
	}
	return text
}
