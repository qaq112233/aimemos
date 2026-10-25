package config

import (
	"reflect"
	"testing"
)

func TestAccessRequiresExplicitConfiguration(t *testing.T) {
	for _, values := range []map[string]string{
		{},
		{"ALLOWED_USERNAMES": " ", "ALLOWED_USER_IDS": "", "TELEGRAM_USER_ID": ""},
		{"ALLOWED_USERNAMES": ", ,"},
		{"ALLOWED_USER_IDS": ", ,"},
		{"ALLOWED_USERNAMES": "display name"},
		{"ALLOWED_USERNAMES": "https://t.me/alice"},
		{"ALLOWED_USERNAMES": "alice,none"},
		{"ALLOWED_USER_IDS": "42,none"},
		{"ALLOWED_USER_IDS": "0"},
		{"ALLOWED_USER_IDS": "-42"},
		{"ALLOWED_USER_IDS": "42,invalid"},
		{"ALLOWED_USER_IDS": "9223372036854775808"},
		{"TELEGRAM_USER_ID": "your-id"},
		{"ALLOWED_USERNAMES": "none", "ALLOWED_USER_IDS": "42"},
		{"ALLOWED_USERNAMES": "alice", "TELEGRAM_USER_ID": "none"},
		{"ALLOWED_USERNAMES": "none", "ALLOWED_USER_IDS": "none"},
	} {
		t.Run(formatAccessValues(values), func(t *testing.T) {
			if _, err := readAccess(env(values), ""); err == nil {
				t.Fatal("accepted missing, invalid or ambiguous access configuration")
			}
			values["SERVER_ADDR"], values["BOT_TOKEN"] = "http://127.0.0.1:1", "test-token"
			if _, err := FromEnv(env(values)); err == nil {
				t.Fatal("startup accepted invalid access configuration")
			}
		})
	}
}

func TestAccessNamesIDsAndExplicitNone(t *testing.T) {
	for _, tc := range []struct {
		name, alias string
		values      map[string]string
		want        Access
	}{
		{"usernames", "", map[string]string{"ALLOWED_USERNAMES": " @Alice, BOB,alice, "}, Access{Usernames: []string{"alice", "bob"}}},
		{"ids", "", map[string]string{"ALLOWED_USER_IDS": "42,43,42"}, Access{UserIDs: []int64{42, 43}}},
		{"legacy id", "", map[string]string{"TELEGRAM_USER_ID": "42"}, Access{UserIDs: []int64{42}}},
		{"custom legacy id", "OWNER_ID", map[string]string{"OWNER_ID": "42"}, Access{UserIDs: []int64{42}}},
		{"combined", "", map[string]string{"ALLOWED_USERNAMES": "alice", "ALLOWED_USER_IDS": "42", "TELEGRAM_USER_ID": "43"}, Access{Usernames: []string{"alice"}, UserIDs: []int64{42, 43}}},
		{"none in names", "", map[string]string{"ALLOWED_USERNAMES": "none"}, Access{AllowAll: true}},
		{"none in ids", "", map[string]string{"ALLOWED_USER_IDS": "none"}, Access{AllowAll: true}},
		{"none in legacy id", "", map[string]string{"TELEGRAM_USER_ID": " none "}, Access{AllowAll: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readAccess(env(tc.values), tc.alias)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v %v, want %+v", got, err, tc.want)
			}
		})
	}
	access := Access{Usernames: []string{"alice"}, UserIDs: []int64{42}}
	if !access.Allows(42, "") || !access.Allows(99, "Alice") || access.Allows(99, "mallory") || access.Allows(99, "") {
		t.Fatal("access must match either an allowed ID or username")
	}
	if (Access{}).Allows(42, "alice") || !(Access{AllowAll: true}).Allows(99, "") {
		t.Fatal("zero-value access or explicit unrestricted policy is incorrect")
	}
}

func TestNotebookStartupUsesCommonAccessSettings(t *testing.T) {
	for _, field := range []string{"ALLOWED_USERNAMES", "ALLOWED_USER_IDS", "TELEGRAM_USER_ID"} {
		for _, value := range []string{"42", "none"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				values := map[string]string{"SERVER_ADDR": "http://127.0.0.1:1", "BOT_TOKEN": "token", "MEMOS_TOKEN": "memos-token", "NOTEBOOK_CONFIG": "../../notebook.example.yaml", field: value}
				cfg, err := FromEnv(env(values))
				if err != nil || cfg.Notebook == nil || !reflect.DeepEqual(cfg.Access, cfg.Notebook.Access) || cfg.Access.AllowAll != (value == "none") {
					t.Fatalf("notebook access configuration not connected: %+v %v", cfg, err)
				}
			})
		}
	}
}

func formatAccessValues(values map[string]string) string {
	return values["ALLOWED_USERNAMES"] + "/" + values["ALLOWED_USER_IDS"] + "/" + values["TELEGRAM_USER_ID"]
}
