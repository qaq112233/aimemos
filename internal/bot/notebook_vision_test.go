package bot

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/usememos/memogram/internal/ai"
	"github.com/usememos/memogram/internal/config"
	"github.com/usememos/memogram/internal/memosapi"
	"github.com/usememos/memogram/internal/workflow"
)

func TestNotebookVisionProtocolsEditRetryAndSave(t *testing.T) {
	// A real one-pixel PNG, served with a misleading download Content-Type.
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"responses", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			cfg := notebookSettings(t)
			tg := newFakeTelegram(t, map[string]fakeFile{
				"photo-secret-id": {path: "photos/one.png", contentType: "application/octet-stream", content: png},
				"doc-secret-id":   {path: "documents/two.png", contentType: "image/jpeg", content: png},
				"pdf-secret-id":   {path: "documents/info.pdf", contentType: "application/pdf", content: []byte("%PDF-fixture")},
			})
			var calls, creates, uploads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				wantPath := "/v1/responses"
				if protocol == "chat_completions" {
					wantPath = "/v1/chat/completions"
				}
				if r.URL.Path != wantPath || creates.Load() != 0 || uploads.Load() != 0 {
					t.Error("incorrect protocol routing or Memos write before confirmation")
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				body, _ := json.Marshal(request)
				for _, secret := range []string{"secret-id", "123:test-token", "/file/bot", "unit-memos-token"} {
					if strings.Contains(string(body), secret) {
						t.Errorf("credentials or file identifiers reached AI: %s", secret)
					}
				}
				var parts []any
				if protocol == "responses" {
					parts = request["input"].([]any)[0].(map[string]any)["content"].([]any)
				} else {
					parts = request["messages"].([]any)[1].(map[string]any)["content"].([]any)
				}
				if len(parts) != 5 {
					t.Errorf("expected material text and two labelled images, got %d parts", len(parts))
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				var input struct {
					Materials    []struct{ Text string } `json:"materials"`
					Draft        string                  `json:"current_draft"`
					Requirements []string                `json:"user_requirements_in_order"`
				}
				if err := json.Unmarshal([]byte(parts[0].(map[string]any)["text"].(string)), &input); err != nil {
					t.Error(err)
				}
				if len(input.Materials) != 3 || input.Materials[0].Text != "第一张说明" || input.Materials[1].Text != "第二张说明" {
					t.Error("lost image captions or material order")
				}
				if n > 1 && (input.Draft != "图片笔记 #图片" || len(input.Requirements) != 1 || input.Requirements[0] != "请补充图片中的文字") {
					t.Error("lost draft/edit context during image revision or retry")
				}
				for i := range 2 {
					label := parts[1+i*2].(map[string]any)["text"].(string)
					if !strings.Contains(label, fmt.Sprintf("materials[%d].Attachments[0]", i)) {
						t.Errorf("wrong image/caption association: %s", label)
					}
					image := parts[2+i*2].(map[string]any)
					url, _ := image["image_url"].(string)
					if protocol == "chat_completions" {
						url = image["image_url"].(map[string]any)["url"].(string)
					}
					if url != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png) {
						t.Error("image bytes changed or sender's misleading MIME type used")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if n == 2 {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":{"message":"provider-private-error"}}`)
					return
				}
				if protocol == "responses" {
					fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"图片笔记 #图片"}]}]}`)
				} else {
					fmt.Fprint(w, `{"choices":[{"message":{"content":"图片笔记 #图片"},"finish_reason":"stop"}]}`)
				}
			}))
			defer server.Close()
			cfg.Providers["cli_proxy"] = config.Provider{BaseURL: server.URL + "/v1", Timeout: 3 * time.Second}
			model := cfg.Models["primary"]
			model.Protocol = protocol
			cfg.Models["primary"] = model
			client := fakeMemos(t, map[string]http.HandlerFunc{
				"POST /api/memos": func(w http.ResponseWriter, r *http.Request) {
					creates.Add(1)
					var memo memosapi.Memo
					if err := json.NewDecoder(r.Body).Decode(&memo); err != nil || memo.Content != "图片笔记 #图片" {
						t.Errorf("wrong saved image draft: %+v %v", memo, err)
					}
					fmt.Fprint(w, `{"name":"memos/vision"}`)
				},
				"POST /api/attachments": func(w http.ResponseWriter, r *http.Request) {
					uploads.Add(1)
					var a memosapi.Attachment
					if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
						t.Error(err)
					}
					want := base64.StdEncoding.EncodeToString(png)
					if a.Filename == "info.pdf" {
						want = base64.StdEncoding.EncodeToString([]byte("%PDF-fixture"))
					}
					if a.Memo != "memos/vision" || a.Content != want {
						t.Error("attachment bytes or memo association changed")
					}
					fmt.Fprint(w, `{"name":"attachments/vision"}`)
				},
			})
			s := newTestService(t, tg, Options{Notebook: cfg, Memos: client, NewGenerator: func(fetch workflow.Fetch) workflow.Generator { return ai.New(cfg, fetch) }})
			photo := notebookMessage(1, "")
			photo.Message.Photo = []models.PhotoSize{{FileID: "photo-secret-id"}}
			photo.Message.Caption, photo.Message.MediaGroupID = "第一张说明", "album"
			s.process(photo)
			doc := notebookMessage(2, "")
			doc.Message.Document = &models.Document{FileID: "doc-secret-id", FileName: "SCREENSHOT.PNG"}
			doc.Message.Caption = "第二张说明"
			s.process(doc)
			pdf := notebookMessage(3, "")
			pdf.Message.Document = &models.Document{FileID: "pdf-secret-id", FileName: "info.pdf", MimeType: "application/pdf"}
			s.process(pdf)
			if v := s.notebook.Status(notebookKey(42)); v.Images != 2 || v.Files != 1 || calls.Load() != 0 || tg.downloadCount() != 0 {
				t.Fatalf("incorrect collection or eager download: %+v", v)
			}
			s.process(notebookMessage(4, "5"))
			awaitNotebook(t, tg, "草稿 v1；")
			if tg.downloadCount() != 2 || creates.Load() != 0 || uploads.Load() != 0 {
				t.Fatal("vision downloaded non-images or saved before confirmation")
			}
			s.process(notebookMessage(5, "请补充图片中的文字"))
			awaitNotebook(t, tg, "AI 生成失败")
			v := s.notebook.Status(notebookKey(42))
			if v.Count != 3 || v.Draft.Version != 1 || v.Draft.Text != "图片笔记 #图片" {
				t.Fatal("provider failure lost sources or previous draft")
			}
			s.process(notebookMessage(6, "/retry"))
			awaitNotebook(t, tg, "草稿 v2；")
			s.process(notebookButton(42, fmt.Sprintf("nb:save:%s:1", v.Group)))
			if creates.Load() != 0 {
				t.Fatal("stale image draft saved")
			}
			callback := fmt.Sprintf("nb:save:%s:2", v.Group)
			s.process(notebookButton(42, callback))
			s.process(notebookButton(42, callback))
			awaitNotebook(t, tg, "保存成功")
			if creates.Load() != 1 || uploads.Load() != 3 || calls.Load() != 3 || tg.downloadCount() != 9 {
				t.Fatalf("wrong save/download counts: creates=%d uploads=%d ai=%d downloads=%d", creates.Load(), uploads.Load(), calls.Load(), tg.downloadCount())
			}
		})
	}
}

func TestNotebookVisionFailuresKeepMaterials(t *testing.T) {
	for _, tc := range []struct {
		name, notice string
		file         fakeFile
		model        string
		limit        int
	}{
		{name: "download", file: fakeFile{path: "photos/fail.png", status: 502}, notice: "图片下载失败"},
		{name: "oversized", file: fakeFile{path: "photos/large.png", reportedSize: maxDownloadBytes + 1}, notice: "图片下载失败"},
		{name: "unsupported bytes", file: fakeFile{path: "photos/pretend.png", content: []byte("<svg></svg>")}, notice: "图片格式不受支持"},
		{name: "text model", model: "alternate", notice: "此模型不支持图片"},
		{name: "image count", limit: 1, notice: "图片数量超过 max_images"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := notebookSettings(t)
			if tc.limit > 0 {
				cfg.MaxImages = tc.limit
			}
			tg := newFakeTelegram(t, map[string]fakeFile{"p": tc.file})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"仅整理说明"}]}]}`)
			}))
			defer server.Close()
			cfg.Providers["cli_proxy"] = config.Provider{BaseURL: server.URL, Timeout: time.Second}
			s := newTestService(t, tg, Options{Notebook: cfg, NewGenerator: func(fetch workflow.Fetch) workflow.Generator { return ai.New(cfg, fetch) }})
			for i := 1; i <= 2; i++ {
				u := notebookMessage(i, "")
				u.Message.Photo = []models.PhotoSize{{FileID: "p"}}
				u.Message.Caption = "保留说明"
				s.process(u)
			}
			if tc.model != "" {
				s.process(notebookMessage(3, "/model "+tc.model))
			}
			s.process(notebookMessage(4, "5"))
			awaitNotebook(t, tg, tc.notice)
			v := s.notebook.Status(notebookKey(42))
			if v.Count != 2 || v.Draft.Version != 0 || v.Generating || calls.Load() != 0 {
				t.Fatalf("invalid image reached AI or lost collection: %+v calls=%d", v, calls.Load())
			}
			// The same sources remain usable with a profile that skips pixels.
			downloads := tg.downloadCount()
			s.process(notebookMessage(5, "/model primary"))
			s.process(notebookMessage(6, "2"))
			awaitNotebook(t, tg, "草稿 v1；")
			if calls.Load() != 1 || tg.downloadCount() != downloads {
				t.Fatal("text-only recovery downloaded images")
			}
		})
	}
}

func TestNotebookImageDocumentDetection(t *testing.T) {
	for _, tc := range []struct {
		name, mime string
		image      bool
	}{
		{"photo.JPG", "", true}, {"photo.webp", "application/octet-stream", true},
		{"photo", "image/png; charset=binary", true}, {"photo", "IMAGE/JPEG", true},
		{"photo.heic", "", true}, {"photo.svg", "image/svg+xml", true},
		{"report.pdf", "application/pdf", false}, {"report.png.txt", "text/plain", false},
	} {
		t.Run(tc.name+"/"+tc.mime, func(t *testing.T) {
			u := notebookMessage(1, "")
			u.Message.Document = &models.Document{FileID: "id", FileName: tc.name, MimeType: tc.mime}
			m, ok := notebookMaterial(u.Message)
			if !ok || len(m.Attachments) != 1 || m.Attachments[0].Image != tc.image {
				t.Fatalf("wrong image document detection: %+v", m)
			}
		})
	}
}
