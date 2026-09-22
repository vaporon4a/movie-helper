package config

import "testing"

func TestConfiguration(t *testing.T) {
	base := map[string]string{"BOT_TOKEN": "123:test", "ALLOWED_CHAT_IDS": "-123, -456"}
	get := func(k string) string { return base[k] }
	c, err := Parse(get)
	if err != nil || len(c.Chats) != 2 || len(c.FactWikiTitles) != 20 || c.GeminiDailyLimit != 6 || c.GeminiKey != "" {
		t.Fatalf("unexpected defaults %#v %v", c, err)
	}
	for _, tc := range []struct{ k, v string }{{"BOT_TOKEN", ""}, {"ALLOWED_CHAT_IDS", "1"}, {"GEMINI_MODEL", "bad/model"}, {"GEMINI_DAILY_REQUEST_LIMIT", "-1"}, {"GEMINI_DAILY_REQUEST_LIMIT", "101"}, {"FACT_WIKI_TITLES", "|"}, {"MEME_SUBREDDITS", "../../bad"}} {
		t.Run(tc.k+tc.v, func(t *testing.T) {
			old := base[tc.k]
			base[tc.k] = tc.v
			defer func() { base[tc.k] = old }()
			if _, err := Parse(get); err == nil {
				t.Fatal("invalid value accepted")
			}
		})
	}
	base["GEMINI_DAILY_REQUEST_LIMIT"] = "0"
	base["FACT_WIKI_TITLES"] = "Alien (film)|Alien (film)|The Matrix"
	c, err = Parse(get)
	if err != nil || c.GeminiDailyLimit != 0 || len(c.FactWikiTitles) != 2 {
		t.Fatal(c, err)
	}
}
