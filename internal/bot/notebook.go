package bot

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/usememos/memogram/internal/config"
	"github.com/usememos/memogram/internal/memos"
	"github.com/usememos/memogram/internal/memosapi"
	"github.com/usememos/memogram/internal/workflow"
)

func (s *Service) notebookAccess(u *models.Update) bool {
	if m := u.Message; m != nil {
		return m.From != nil && m.Chat.ID == m.From.ID && m.Chat.Type == models.ChatTypePrivate && s.access.Allows(m.From.ID, m.From.Username)
	}
	if q := u.CallbackQuery; q != nil {
		m := q.Message.Message
		return m != nil && m.Chat.ID == q.From.ID && m.Chat.Type == models.ChatTypePrivate && s.access.Allows(q.From.ID, q.From.Username)
	}
	return false
}

func notebookKey(chatID int64) string { return fmt.Sprintf("telegram:private:%d", chatID) }

func (s *Service) handleNotebookMessage(ctx context.Context, u *models.Update) {
	m := u.Message
	if m == nil {
		return
	}
	key, text := notebookKey(m.Chat.ID), m.Text
	v := s.notebook.Status(key)
	ownText := m.ForwardOrigin == nil && text != "" && len(m.Photo) == 0 && m.Document == nil && m.Video == nil && m.Voice == nil
	if ownText {
		switch {
		case text == "/start" || text == "/status":
			s.showNotebook(ctx, m.Chat.ID, v, true)
			return
		case text == "/model":
			names := make([]string, 0, len(s.notebookCfg.Models))
			for name := range s.notebookCfg.Models {
				names = append(names, name)
			}
			slices.Sort(names)
			s.reply(ctx, m.Chat.ID, "模型："+strings.Join(names, ", ")+"\n发送 /model 配置名称 切换。")
			return
		case strings.HasPrefix(text, "/model "):
			v, err := s.notebook.SelectModel(key, strings.TrimSpace(strings.TrimPrefix(text, "/model ")))
			s.notebookReply(ctx, m.Chat.ID, v, nil, err, false)
			return
		case text == "/retry":
			v, work, err := s.notebook.BeginGenerate(key, v.Group, "", "", config.Parameters{})
			s.notebookReply(ctx, m.Chat.ID, v, work, err, true)
			return
		case strings.HasPrefix(text, "/"):
			s.reply(ctx, m.Chat.ID, "请用 /status 查看当前组，/model 切换模型，或使用按钮操作。")
			return
		}
		if v.Collecting && !v.Generating {
			if entry, prompt, ok := parseEntry(s.notebookCfg, text); ok {
				s.runEntry(ctx, m.Chat.ID, key, v.Group, entry, prompt)
				return
			}
		}
		if v.Draft.Version > 0 && !v.Collecting && !v.Generating {
			v, work, err := s.notebook.BeginGenerate(key, v.Group, "", text, config.Parameters{})
			s.notebookReply(ctx, m.Chat.ID, v, work, err, true)
			return
		}
	}
	material, ok := notebookMaterial(m)
	if !ok {
		s.reply(ctx, m.Chat.ID, "请发送文字、链接、图片或文件作为素材")
		return
	}
	v, err := s.notebook.Add(key, material)
	if err != nil {
		s.reply(ctx, m.Chat.ID, err.Error())
		return
	}
	// This interval throttles status only. It never triggers generation/save
	// or splits an album/group. /status always shows the exact count.
	if v.Count == 1 || v.Generating || v.Draft.Version > 0 || time.Since(s.statusAt[m.Chat.ID]) >= s.notebookCfg.StatusEvery {
		s.statusAt[m.Chat.ID] = time.Now()
		s.showNotebook(ctx, m.Chat.ID, v, false)
	}
}

func parseEntry(cfg *config.Notebook, text string) (config.Entry, string, bool) {
	if e, ok := cfg.Entries[text]; ok && !e.Custom {
		return e, "", true
	}
	key, prompt, ok := strings.Cut(text, " ")
	e, exists := cfg.Entries[key]
	if ok && exists && e.Custom && strings.TrimSpace(prompt) != "" {
		return e, strings.TrimSpace(prompt), true
	}
	return config.Entry{}, "", false
}

func (s *Service) runEntry(ctx context.Context, chatID int64, key, id string, entry config.Entry, prompt string) {
	if entry.Action == "raw" {
		v, work, err := s.notebook.BeginSave(key, id, 0, true)
		s.notebookReply(ctx, chatID, v, work, err, false)
		return
	}
	v, work, err := s.notebook.BeginGenerate(key, id, entry.Profile, prompt, config.Parameters{})
	s.notebookReply(ctx, chatID, v, work, err, true)
}

func (s *Service) handleNotebookCallback(ctx context.Context, _ *bot.Bot, u *models.Update) {
	q := u.CallbackQuery
	s.answer(ctx, q.ID, "")
	parts := strings.Split(q.Data, ":")
	if len(parts) != 4 || parts[0] != "nb" {
		s.reply(ctx, q.From.ID, "按钮已过期，请使用 /status")
		return
	}
	chatID, key, id := q.From.ID, notebookKey(q.From.ID), parts[2]
	v := s.notebook.Status(key)
	if id != v.Group || id == "" {
		s.reply(ctx, chatID, "本组已结束或按钮已过期")
		return
	}
	var err error
	switch parts[1] {
	case "entry":
		entry, ok := s.notebookCfg.Entries[parts[3]]
		if !ok {
			return
		}
		if !v.Collecting {
			s.reply(ctx, chatID, "请使用最新草稿的按钮，或先点击补充素材")
			return
		}
		if entry.Custom {
			s.reply(ctx, chatID, "请发送 "+parts[3]+" 本次提示词")
			return
		}
		s.runEntry(ctx, chatID, key, id, entry, "")
		return
	case "save":
		version, parseErr := strconv.Atoi(parts[3])
		if parseErr != nil {
			return
		}
		v, work, err := s.notebook.BeginSave(key, id, version, version == 0)
		s.notebookReply(ctx, chatID, v, work, err, false)
		return
	case "collect":
		v, err = s.notebook.Collect(key, id)
	case "discard":
		v, err = s.notebook.Discard(key, id)
	default:
		return
	}
	s.notebookReply(ctx, chatID, v, nil, err, false)
}

func (s *Service) notebookReply(ctx context.Context, chatID int64, v workflow.View, work func(context.Context) workflow.View, err error, preview bool) {
	if err != nil {
		s.reply(ctx, chatID, err.Error())
		return
	}
	s.showNotebook(ctx, chatID, v, false)
	if work != nil {
		go func() { result := work(ctx); s.showNotebook(ctx, chatID, result, preview) }()
	}
}

func (s *Service) showNotebook(ctx context.Context, chatID int64, v workflow.View, preview bool) {
	if preview && v.Draft.Version > 0 {
		// Plain text prevents arbitrary model Markdown from becoming Telegram
		// entities. Each chunk is below 4096 UTF-16 units, even with emoji.
		runes := []rune(v.Draft.Text)
		for start := 0; start < len(runes); start += 1800 {
			s.reply(ctx, chatID, string(runes[start:min(start+1800, len(runes))]))
		}
	}
	text := v.Notice
	if v.Group == "" {
		if text == "" {
			text = "发送素材开始收集；选择处理方式前不会写入 Memos。"
		}
		s.reply(ctx, chatID, text)
		return
	}
	text += fmt.Sprintf("\n已收集 %d 条素材（图片 %d，其他附件 %d）", v.Count, v.Images, v.Files)
	if v.Draft.Version > 0 {
		text += fmt.Sprintf("\n草稿 v%d；包含 %d 条素材", v.Draft.Version, v.Draft.Count)
	}
	if v.Pending > 0 {
		text += fmt.Sprintf("\n新增 %d 条素材尚未纳入；确认仅保存此版本，新增素材保留为下一组", v.Pending)
	}
	if v.Model != "" {
		text += "\n当前模型：" + v.Model
	}
	button := func(label, action, arg string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: label, CallbackData: "nb:" + action + ":" + v.Group + ":" + arg}
	}
	var rows [][]models.InlineKeyboardButton
	if !v.Generating && !v.Saving {
		if v.Partial {
			rows = append(rows, []models.InlineKeyboardButton{button("重试补传附件", "save", strconv.Itoa(v.Draft.Version))})
		} else {
			if v.Collecting {
				keys := make([]string, 0, len(s.notebookCfg.Entries))
				for key := range s.notebookCfg.Entries {
					keys = append(keys, key)
				}
				slices.Sort(keys)
				for _, key := range keys {
					rows = append(rows, []models.InlineKeyboardButton{button(key+" "+s.notebookCfg.Entries[key].Label, "entry", key)})
				}
			}
			if v.Draft.Version > 0 && !v.Draft.Truncated {
				rows = append(rows, []models.InlineKeyboardButton{button(fmt.Sprintf("确认保存 v%d", v.Draft.Version), "save", strconv.Itoa(v.Draft.Version))})
			}
			if v.Draft.Version > 0 {
				rows = append(rows, []models.InlineKeyboardButton{button("补充素材", "collect", "-")})
			}
		}
		rows = append(rows, []models.InlineKeyboardButton{button("放弃本组", "discard", "-")})
	}
	s.send(ctx, &bot.SendMessageParams{ChatID: chatID, Text: strings.TrimSpace(text), ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: rows}})
}

var sourceURL = regexp.MustCompile(`https?://[^\s<>]+`)

func sourceLinks(text string, entities []models.MessageEntity) []string {
	var links []string
	add := func(link string) {
		if u, err := url.Parse(link); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && !slices.Contains(links, link) {
			links = append(links, link)
		}
	}
	// Telegram's URL spans are authoritative and count UTF-16 units. Mask them
	// before fallback extraction to avoid also adding a shortened duplicate.
	units := utf16.Encode([]rune(text))
	uncovered := slices.Clone(units)
	for _, entity := range entities {
		if entity.Type != models.MessageEntityTypeURL || entity.Offset < 0 || entity.Length <= 0 || entity.Offset > len(units) || entity.Length > len(units)-entity.Offset {
			continue
		}
		end := entity.Offset + entity.Length
		add(string(utf16.Decode(units[entity.Offset:end])))
		for i := entity.Offset; i < end; i++ {
			uncovered[i] = ' '
		}
	}
	for _, link := range sourceURL.FindAllString(string(utf16.Decode(uncovered)), -1) {
		link = strings.TrimRight(link, ".,;!?，。；！？\"'”’")
		// Remove surrounding prose/Markdown delimiters while retaining balanced
		// parentheses and IPv6 brackets within the URL itself.
		for len(link) > 0 {
			last := link[len(link)-1]
			open := byte(0)
			switch last {
			case ')':
				open = '('
			case ']':
				open = '['
			case '}':
				open = '{'
			}
			if open == 0 || strings.Count(link, string(last)) <= strings.Count(link, string(open)) {
				break
			}
			link = link[:len(link)-1]
		}
		add(link)
	}
	return links
}

func notebookMaterial(m *models.Message) (workflow.Material, bool) {
	// Reuse upstream forwarding/attachment helpers, preserving original text
	// bytes instead of converting raw message entities into new Markdown.
	text := m.Text
	entities := m.Entities
	if m.Caption != "" {
		text, entities = m.Caption, m.CaptionEntities
	}
	sources := sourceLinks(text, entities)
	for _, entity := range entities {
		if entity.Type == models.MessageEntityTypeTextLink {
			if u, err := url.Parse(entity.URL); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
				if !slices.Contains(sources, entity.URL) {
					sources = append(sources, entity.URL)
				}
				if !strings.Contains(text, entity.URL) {
					text += "\n" + entity.URL
				}
			}
		}
	}
	if m.ForwardOrigin != nil {
		text = forwardHeader(m.ForwardOrigin) + "\n" + text
		sources = append(sources, sourceLinks(forwardHeader(m.ForwardOrigin), nil)...)
		if ch := m.ForwardOrigin.MessageOriginChannel; ch != nil && ch.Chat.Username != "" {
			link := fmt.Sprintf("https://t.me/%s/%d", ch.Chat.Username, ch.MessageID)
			sources = append(sources, link)
			text += "\n" + link
		}
	}
	material := workflow.Material{ID: m.ID, Text: text, Sources: sources}
	if file, ok := messageFile(m); ok {
		image := len(m.Photo) > 0 || notebookImage(file)
		material.Attachments = []workflow.Attachment{{ID: file.id, Name: file.name, MIME: file.mimeType, Image: image}}
	}
	return material, text != "" || len(material.Attachments) > 0
}

// Some Telegram clients send image documents without a MIME type. Use their
// filename only to select downloads; the AI client checks the actual bytes.
func notebookImage(file telegramFile) bool {
	mediaType, _, _ := mime.ParseMediaType(file.mimeType)
	if strings.HasPrefix(strings.ToLower(mediaType), "image/") {
		return true
	}
	switch strings.ToLower(path.Ext(file.name)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".heic", ".heif", ".bmp", ".tif", ".tiff":
		return true
	}
	return false
}

func (s *Service) fetchNotebookFile(ctx context.Context, a workflow.Attachment) (workflow.File, error) {
	info, err := s.bot.GetFile(ctx, &bot.GetFileParams{FileID: a.ID})
	if err != nil {
		return workflow.File{}, redactTelegramError(err)
	}
	data, mime, err := s.download(ctx, info)
	if err != nil {
		return workflow.File{}, err
	}
	file := telegramFile{id: a.ID, name: a.Name, mimeType: a.MIME}
	return workflow.File{Name: attachmentName(file, info), MIME: attachmentType(mime, a.MIME, data), Data: data}, nil
}

type notebookDestination struct{ s *Service }

func (d notebookDestination) Create(ctx context.Context, content string) (workflow.Memo, error) {
	memo, err := d.s.memos.WithAccessToken(d.s.notebookCfg.MemosToken).CreateMemo(ctx, memosapi.Memo{Content: content})
	if err != nil {
		return workflow.Memo{}, errors.New("请检查 Memos 连接和访问令牌")
	}
	id, err := memos.MemoUID(memo.Name)
	if err != nil {
		return workflow.Memo{}, errors.New("返回了无效的 Memos 笔记标识")
	}
	return workflow.Memo{Name: memo.Name, URL: d.s.linkBase + "/memos/" + id}, nil
}
func (d notebookDestination) Upload(ctx context.Context, memo workflow.Memo, a workflow.Attachment) error {
	return d.s.saveAttachment(ctx, d.s.memos.WithAccessToken(d.s.notebookCfg.MemosToken), telegramFile{id: a.ID, name: a.Name, mimeType: a.MIME}, &memosapi.Memo{Name: memo.Name})
}
