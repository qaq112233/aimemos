package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"go.yaml.in/yaml/v3"

	"github.com/usememos/memogram/internal/config"
	"github.com/usememos/memogram/internal/memosapi"
	"github.com/usememos/memogram/internal/workflow"
)

type notebookGenerator func(context.Context, workflow.Request) (workflow.Result, error)

func (f notebookGenerator) Generate(ctx context.Context, r workflow.Request) (workflow.Result, error) {
	return f(ctx, r)
}
func notebookSettings(t *testing.T) *config.Notebook {
	t.Helper()
	data, err := os.ReadFile("../../notebook.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Notebook
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.UserID, cfg.MemosToken = 42, "unit-memos-token"
	return &cfg
}
func notebookMessage(id int, text string) *models.Update {
	m := textMessage(42, text)
	m.ID = id
	return &models.Update{Message: m}
}
func notebookButton(user int64, data string) *models.Update {
	return &models.Update{CallbackQuery: &models.CallbackQuery{ID: "q", From: models.User{ID: user}, Data: data, Message: models.MaybeInaccessibleMessage{Message: textMessage(42, "")}}}
}
func awaitNotebook(t *testing.T, tg *fakeTelegram, text string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, call := range tg.snapshot() {
			if strings.Contains(call.text, text) {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("missing Telegram text %q: %+v", text, tg.snapshot())
}

func TestNotebookTelegramCollectDraftAndSave(t *testing.T) {
	cfg := notebookSettings(t)
	tg := newFakeTelegram(t, map[string]fakeFile{
		"p1": {path: "photos/one.jpg", contentType: "image/jpeg", content: []byte("one")},
		"p2": {path: "photos/two.jpg", contentType: "image/jpeg", content: []byte("two")}})
	var creates, uploads, aiCalls atomic.Int32
	client := fakeMemos(t, map[string]http.HandlerFunc{
		"POST /api/memos": func(w http.ResponseWriter, r *http.Request) {
			creates.Add(1)
			if r.Header.Get("Authorization") != "Bearer unit-memos-token" {
				t.Error("missing Memos auth")
			}
			var memo memosapi.Memo
			if err := json.NewDecoder(r.Body).Decode(&memo); err != nil {
				t.Error(err)
			}
			if !strings.Contains(memo.Content, "#Go") || strings.Contains(memo.Content, "raw initial paragraph") {
				t.Errorf("bad draft content: %s", memo.Content)
			}
			fmt.Fprint(w, `{"name":"memos/note","content":"organized #Go"}`)
		},
		"POST /api/attachments": func(w http.ResponseWriter, r *http.Request) {
			uploads.Add(1)
			var a memosapi.Attachment
			if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
				t.Error(err)
			}
			if a.Memo != "memos/note" || !strings.HasSuffix(a.Filename, ".jpg") {
				t.Errorf("lost attachment ownership or filename: %+v", a)
			}
			fmt.Fprint(w, `{"name":"attachments/a"}`)
		}})
	s := newTestService(t, tg, Options{Notebook: cfg, Memos: client, NewGenerator: func(workflow.Fetch) workflow.Generator {
		return notebookGenerator(func(_ context.Context, r workflow.Request) (workflow.Result, error) {
			aiCalls.Add(1)
			if r.Profile.Images || len(r.Materials) != 4 || r.Materials[1].Text != "Forwarded from source\n1" || r.Materials[2].Text != "3" {
				t.Errorf("bad material conversion: %+v", r)
			}
			return workflow.Result{Text: "organized #Go"}, nil
		})
	}})
	s.process(notebookMessage(1, "raw initial paragraph https://source.test"))
	forward := notebookMessage(2, "1")
	forward.Message.ForwardOrigin = &models.MessageOrigin{MessageOriginHiddenUser: &models.MessageOriginHiddenUser{SenderUserName: "source"}}
	s.process(forward)
	for i, id := range []string{"p1", "p2"} {
		u := notebookMessage(3+i, "")
		u.Message.MediaGroupID = "album"
		u.Message.Photo = []models.PhotoSize{{FileID: id}}
		if i == 0 {
			u.Message.Caption = "3"
		}
		s.process(u)
	}
	if v := s.notebook.Status(notebookKey(42)); v.Count != 4 || v.Images != 2 {
		t.Fatalf("wrong collection: %+v", v)
	}
	if creates.Load() != 0 || uploads.Load() != 0 || tg.downloadCount() != 0 {
		t.Fatal("collection performed writes/downloads")
	}
	s.process(notebookMessage(5, "2"))
	awaitNotebook(t, tg, "草稿已更新")
	if creates.Load() != 0 || tg.downloadCount() != 0 || aiCalls.Load() != 1 {
		t.Fatal("tidy downloaded images or saved early")
	}
	v := s.notebook.Status(notebookKey(42))
	callback := fmt.Sprintf("nb:save:%s:%d", v.Group, v.Draft.Version)
	s.process(notebookButton(42, callback))
	s.process(notebookButton(42, callback))
	awaitNotebook(t, tg, "保存成功")
	s.process(notebookButton(42, callback))
	if creates.Load() != 1 || uploads.Load() != 2 || tg.downloadCount() != 2 {
		t.Fatalf("save duplicated: creates=%d uploads=%d downloads=%d", creates.Load(), uploads.Load(), tg.downloadCount())
	}
}

func TestNotebookPrivateUserRestrictionAndExactEntryRules(t *testing.T) {
	cfg := notebookSettings(t)
	tg := newFakeTelegram(t, nil)
	s := newTestService(t, tg, Options{Notebook: cfg})
	foreign := notebookMessage(1, "secret")
	foreign.Message.From.ID = 99
	s.process(foreign)
	group := notebookMessage(1, "secret")
	group.Message.Chat.Type = models.ChatTypeGroup
	group.Message.Chat.ID = -100
	s.process(group)
	s.process(notebookButton(99, "nb:save:x:0"))
	if len(tg.snapshot()) != 0 || s.notebook.Status(notebookKey(42)).Count != 0 {
		t.Fatal("accepted unauthorized or group update")
	}
	for _, text := range []string{"1", "2", "3", "4 custom prompt"} {
		if _, _, ok := parseEntry(cfg, text); !ok {
			t.Errorf("rejected %q", text)
		}
	}
	for _, text := range []string{" 1", "1 ", "11", "2 notes", "4", "4 ", "4bad", "4\nprompt"} {
		if _, _, ok := parseEntry(cfg, text); ok {
			t.Errorf("accepted partial match %q", text)
		}
	}
}
