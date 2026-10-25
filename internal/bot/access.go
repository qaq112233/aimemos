package bot

import (
	"context"
	"log/slog"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// authorize runs before every handler. It drops updates Memogram does not
// handle, such as edits, channel posts and membership changes, and enforces
// the configured access policy for both messages and button presses.
func (s *Service) authorize(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if s.notebook != nil {
			if s.notebookAccess(update) {
				next(ctx, b, update)
			}
			return
		}
		switch {
		case update.Message != nil:
			msg := update.Message
			if msg.From == nil {
				return
			}
			if reason, ok := s.checkAccess(*msg.From); !ok {
				slog.Warn("access denied", slog.String("username", msg.From.Username))
				s.reply(ctx, msg.Chat.ID, "Error: "+reason)
				return
			}
		case update.CallbackQuery != nil:
			query := update.CallbackQuery
			if reason, ok := s.checkAccess(query.From); !ok {
				slog.Warn("access denied", slog.String("username", query.From.Username))
				s.alert(ctx, query.ID, "Error: "+reason)
				return
			}
		default:
			return
		}
		next(ctx, b, update)
	}
}

// checkAccess reports whether a Telegram user may use the bot, and why
// not when it may not.
func (s *Service) checkAccess(user models.User) (string, bool) {
	if s.access.Allows(user.ID, user.Username) {
		return "", true
	}
	return "此 Telegram 账号无权使用机器人", false
}
