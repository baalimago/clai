package cloud

import "testing"

func TestAllowed(t *testing.T) {
	cases := []struct {
		store Store
		path  string
		want  bool
	}{
		{StoreConfig, "textConfig.json", true},
		{StoreConfig, "profiles/coder.json", true},
		{StoreConfig, "profiles/nested/coder.json", false},
		{StoreConfig, "skills/review/SKILL.md", true},
		{StoreConfig, "skills_trust.json", false},
		{StoreConfig, "openai_gpt_gpt-5.json", false},
		{StoreConfig, "cloud/credentials.json", false},
		{StoreConfig, "skills/.hidden", false},
		{StoreConfig, "skills//x", false},
		{StoreConfig, "skills/a\\b", false},
		{StoreConfig, "skills/a\x01b", false},
		{StoreConfig, "", false},
		{StoreConversations, "conversations/abc.json", true},
		{StoreConversations, "conversations/globalScope.json", false},
		{StoreConversations, "conversations/prevQuery.json", false},
		{StoreConversations, "conversations/chat_index.cache", false},
		{StoreConversations, "conversations/dirs/abc.json", false},
		{StoreConversations, "conversations/assets/" + hashBytes(nil) + ".png", true},
		{StoreConversations, "conversations/assets/abc.png", false},
		{StoreConversations, "conversations/reasoning/c1/resp_1.json", true},
		{StoreConversations, "conversations/imported/claude-code/s1.json", true},
		{StoreConversations, "conversations/imported/Claude/s1.json", false},
		{StoreConversations, "profiles/x.json", false},
	}
	for _, c := range cases {
		if got := Allowed(c.store, c.path); got != c.want {
			t.Errorf("Allowed(%s, %q) = %v, want %v", c.store, c.path, got, c.want)
		}
	}
	if s, ok := ParseStore("conversations"); !ok || s != StoreConversations {
		t.Fatal("ParseStore")
	}
	if _, ok := ParseStore("notes"); ok {
		t.Fatal("ParseStore accepted notes")
	}
}

func TestHoldsSecret(t *testing.T) {
	cases := []struct {
		name, body string
		want       bool
	}{
		{"x.json", `{"api_key":"abcdefgh1234"}`, true},
		{"x.json", `{"headers":{"Authorization":"Bearer abcdefgh1234"}}`, true},
		{"x.json", `{"list":[{"password":"hunter22hunter"}]}`, true},
		{"x.json", `{"api_key":"$OPENAI_API_KEY"}`, false},
		{"x.json", `{"api_key":"env:OPENAI"}`, false},
		{"x.json", `{"max_tokens":"12345678"}`, false},
		{"x.json", `{"env":{"GITHUB_TOKEN":"abcdefgh1234"}}`, true},
		{"x.json", `{"x-api-key":"abcdefgh1234"}`, true},
		{"x.json", `{"token_endpoint":"https://example.com/token"}`, false},
		{"x.json", `{"token":"https://example.com/token"}`, false},
		{"x.json", `{"token_budget":4096}`, false},
		{"x.json", `{"note":"short"}`, false},
		{"x.json", `not json`, false},
		{"SKILL.md", "use sk-abcdefghijklmnopqrstuvwxyz", true},
		{"SKILL.md", "api_key: abcdefgh1234", false},
	}
	for _, c := range cases {
		if got, _ := holdsSecret(c.name, []byte(c.body)); got != c.want {
			t.Errorf("holdsSecret(%s, %s) = %v, want %v", c.name, c.body, got, c.want)
		}
	}
}
