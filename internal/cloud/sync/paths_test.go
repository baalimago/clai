package sync

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
