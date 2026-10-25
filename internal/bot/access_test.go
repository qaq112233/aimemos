package bot

import (
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/usememos/memogram/internal/config"
	"github.com/usememos/memogram/internal/memos"
)

func TestIgnoresUpdatesWithoutUserMessage(t *testing.T) {
	tg := newFakeTelegram(t, nil)
	s := newTestService(t, tg, Options{})
	for name, update := range map[string]*models.Update{
		"edited message": {EditedMessage: textMessage(1, "edited")},
		"channel post":   {Message: &models.Message{ID: 1, Chat: models.Chat{ID: -100}, Text: "post"}},
		"membership":     {MyChatMember: &models.ChatMemberUpdated{}},
	} {
		t.Run(name, func(t *testing.T) {
			s.process(update)
		})
	}
	if calls := tg.snapshot(); len(calls) != 0 {
		t.Fatalf("ignored updates produced Telegram calls: %+v", calls)
	}
}

func TestServiceRejectsMissingAccessBeforeConnecting(t *testing.T) {
	_, err := New(Options{Token: "123:token", ServerURL: "http://127.0.0.1:1", Memos: memos.NewClient("http://127.0.0.1:1"), Store: openStore(t)})
	if err == nil || !strings.Contains(err.Error(), "ALLOWED_USERNAMES") {
		t.Fatalf("missing access policy reached Telegram initialization: %v", err)
	}
}

func TestIDAndUnrestrictedAccessForMessagesAndButtons(t *testing.T) {
	for _, access := range []config.Access{{UserIDs: []int64{42}}, {AllowAll: true}} {
		tg := newFakeTelegram(t, nil)
		data := openStore(t)
		bindUser(t, data, 42, "token")
		s := newTestService(t, tg, Options{Store: data, Access: access})
		s.process(&models.Update{Message: textMessage(42, "/start")})
		s.process(&models.Update{CallbackQuery: &models.CallbackQuery{ID: "q", From: models.User{ID: 42}, Data: "invalid"}})
		calls := tg.snapshot()
		if len(calls) != 2 || calls[0].text != "Usage: /start <access_token>" || calls[1].text != "Invalid command" {
			t.Fatalf("allowed ID without a username failed: %+v", calls)
		}
		if !access.AllowAll {
			s.process(&models.Update{Message: textMessage(99, "/start")})
			s.process(&models.Update{CallbackQuery: &models.CallbackQuery{ID: "denied", From: models.User{ID: 99}, Data: "pin memos/a"}})
			calls = tg.snapshot()[2:]
			if len(calls) != 2 || !strings.Contains(calls[0].text, "无权") || !calls[1].alert || !strings.Contains(calls[1].text, "无权") {
				t.Fatalf("foreign ID bypassed message or button access: %+v", calls)
			}
		}
	}
}

func TestAllowedUsernames(t *testing.T) {
	tg := newFakeTelegram(t, nil)
	data := openStore(t)
	bindUser(t, data, 1, "token")
	s := newTestService(t, tg, Options{Store: data, Access: config.Access{Usernames: []string{"alice"}}})
	message := func(username, text string) *models.Update {
		msg := textMessage(1, text)
		msg.From.Username = username
		return &models.Update{Message: msg}
	}
	for _, tc := range []struct {
		name   string
		update *models.Update
		want   telegramCall
	}{
		{"allowed, any case", message("Alice", "/start"), telegramCall{method: "sendMessage", chatID: 1, text: "Usage: /start <access_token>"}},
		{"denied message", message("mallory", "/start token"), telegramCall{method: "sendMessage", chatID: 1, text: "Error: 此 Telegram 账号无权使用机器人"}},
		{"no username", message("", "note"), telegramCall{method: "sendMessage", chatID: 1, text: "Error: 此 Telegram 账号无权使用机器人"}},
		{"denied button", &models.Update{CallbackQuery: &models.CallbackQuery{ID: "q", From: models.User{ID: 1, Username: "mallory"}, Data: "pin memos/abc"}}, telegramCall{method: "answerCallbackQuery", text: "Error: 此 Telegram 账号无权使用机器人", alert: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(tg.snapshot())
			s.process(tc.update)
			calls := tg.snapshot()[before:]
			if len(calls) != 1 || calls[0].method != tc.want.method || calls[0].chatID != tc.want.chatID || calls[0].text != tc.want.text || calls[0].alert != tc.want.alert {
				t.Fatalf("got %+v, want %+v", calls, tc.want)
			}
		})
	}
}
