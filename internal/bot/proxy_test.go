package bot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	telegram "github.com/go-telegram/bot"

	"github.com/usememos/memogram/internal/memos"
	"github.com/usememos/memogram/internal/testproxy"
)

func TestTelegramProxyCoversAPIAndFileDownloads(t *testing.T) {
	for _, protocol := range []string{"http", "socks5"} {
		t.Run(protocol, func(t *testing.T) {
			var mu sync.Mutex
			calls := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls[r.URL.Path]++
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/bot123:test-token/getMe":
					fmt.Fprint(w, `{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"Proxy"}}`)
				case "/bot123:test-token/getFile":
					fmt.Fprint(w, `{"ok":true,"result":{"file_id":"photo","file_path":"photos/test.png","file_size":5}}`)
				case "/file/bot123:test-token/photos/test.png":
					fmt.Fprint(w, "image")
				default:
					t.Errorf("unexpected Telegram path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			proxyURL := server.URL
			if protocol == "socks5" {
				proxyURL = testproxy.NewSOCKS5(t, strings.TrimPrefix(server.URL, "http://"), false).URL
			}
			s, err := New(Options{Token: "123:test-token", ServerURL: "http://telegram.invalid", ProxyURL: proxyURL, Memos: memos.NewClient("http://127.0.0.1:1"), Store: openStore(t), BotOptions: []telegram.Option{telegram.WithNotAsyncHandlers()}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.downloads.CloseIdleConnections()
			info, err := s.bot.GetFile(context.Background(), &telegram.GetFileParams{FileID: "photo"})
			if err != nil {
				t.Fatal(err)
			}
			data, _, err := s.download(context.Background(), info)
			if err != nil || string(data) != "image" {
				t.Fatalf("Telegram attachment bypassed proxy: %q %v", data, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(calls) != 3 {
				t.Fatalf("Telegram initialization/API/download did not share proxy: %v", calls)
			}
		})
	}
}

func TestTelegramRejectsInvalidProxyBeforeConnecting(t *testing.T) {
	_, err := New(Options{Token: "token", ProxyURL: "ftp://user:SECRET@proxy.test", Memos: memos.NewClient("http://127.0.0.1:1"), Store: openStore(t)})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("invalid Telegram proxy accepted or leaked: %v", err)
	}
}
