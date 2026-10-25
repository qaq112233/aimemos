package config

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Access is explicit: its zero value never permits starting the bot.
type Access struct {
	Usernames []string
	UserIDs   []int64
	AllowAll  bool
}

func (a Access) Validate() error {
	if a.AllowAll && (len(a.Usernames) > 0 || len(a.UserIDs) > 0) {
		return errors.New("none 不能与允许用户的名单同时配置")
	}
	if !a.AllowAll && len(a.Usernames) == 0 && len(a.UserIDs) == 0 {
		return errors.New("必须填写 ALLOWED_USERNAMES 或 ALLOWED_USER_IDS（也支持 TELEGRAM_USER_ID）；只有手动填写 none 才不限制用户")
	}
	return nil
}

func (a Access) Allows(id int64, username string) bool {
	return a.AllowAll || slices.Contains(a.UserIDs, id) || (username != "" && slices.Contains(a.Usernames, strings.ToLower(username)))
}

// readAccess keeps the notebook's older user_id_env alias working alongside
// the common access settings. No field defaults to unrestricted access.
func readAccess(getenv func(string) string, userIDEnv string) (Access, error) {
	fields := []string{"ALLOWED_USERNAMES", "ALLOWED_USER_IDS", "TELEGRAM_USER_ID"}
	if userIDEnv != "" && !slices.Contains(fields, userIDEnv) {
		fields = append(fields, userIDEnv)
	}
	var access Access
	configured := 0
	usernamePattern := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	for _, field := range fields {
		raw := strings.TrimSpace(getenv(field))
		if raw == "" {
			continue
		}
		configured++
		if raw == "none" {
			access.AllowAll = true
			continue
		}
		entries := 0
		for entry := range strings.SplitSeq(raw, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if entry == "none" {
				return Access{}, fmt.Errorf("%s: none 必须单独填写，不能混入名单", field)
			}
			entries++
			if field == "ALLOWED_USERNAMES" {
				username := strings.ToLower(strings.TrimPrefix(entry, "@"))
				if !usernamePattern.MatchString(username) {
					return Access{}, errors.New("ALLOWED_USERNAMES 必须是以逗号分隔的 Telegram 用户名，不能填写显示名称或网址")
				}
				if !slices.Contains(access.Usernames, username) {
					access.Usernames = append(access.Usernames, username)
				}
			} else {
				id, err := strconv.ParseInt(entry, 10, 64)
				if err != nil || id <= 0 {
					return Access{}, fmt.Errorf("%s 必须是以逗号分隔的正整数 Telegram 用户 ID", field)
				}
				if !slices.Contains(access.UserIDs, id) {
					access.UserIDs = append(access.UserIDs, id)
				}
			}
		}
		if entries == 0 {
			return Access{}, fmt.Errorf("%s 没有有效的允许用户", field)
		}
	}
	if access.AllowAll && configured != 1 {
		return Access{}, errors.New("none 必须单独配置；请清空其他允许用户字段")
	}
	return access, access.Validate()
}
