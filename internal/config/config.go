package config

import (
	"errors"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	GeminiKey, GeminiModel string
	GeminiDailyLimit       int
	GroqKey, GroqModel     string
	GroqDailyLimit         int
	TMDBToken              string
	FactWikiTitles         []string
	Token, DBPath          string
	Chats                  map[int64]bool
	Subreddits             []string
	Level                  slog.Level
}

func Load() (Config, error) { return Parse(os.Getenv) }

func Parse(get func(string) string) (Config, error) {
	c := Config{Token: strings.TrimSpace(get("BOT_TOKEN")), DBPath: get("DB_PATH"), Chats: make(map[int64]bool)}
	if !regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`).MatchString(c.Token) {
		return c, errors.New("BOT_TOKEN is missing or invalid")
	}
	if c.DBPath == "" {
		c.DBPath = "data/movie-helper.db"
	}
	chats := strings.TrimSpace(get("ALLOWED_CHAT_IDS"))
	// Explicit onboarding mode: /id and private help work, all groups are denied.
	if chats != "bootstrap" {
		for part := range strings.SplitSeq(chats, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil || id >= 0 {
				return c, errors.New("ALLOWED_CHAT_IDS must list negative group IDs or be bootstrap")
			}
			c.Chats[id] = true
		}
	}

	subs := get("MEME_SUBREDDITS")
	if subs == "" {
		subs = "RUSSIANMemeSub"
	}
	for sub := range strings.SplitSeq(subs, ",") {
		sub = strings.TrimSpace(sub)
		if !regexp.MustCompile(`^[A-Za-z0-9_]{2,30}$`).MatchString(sub) {
			return c, errors.New("invalid MEME_SUBREDDITS")
		}
		c.Subreddits = append(c.Subreddits, sub)
	}
	if len(c.Subreddits) > 5 {
		return c, errors.New("MEME_SUBREDDITS allows at most 5 sources")
	}
	if level := get("LOG_LEVEL"); level != "" {
		if err := c.Level.UnmarshalText([]byte(level)); err != nil {
			return c, errors.New("invalid LOG_LEVEL")
		}
	}
	c.GeminiKey = strings.TrimSpace(get("GEMINI_API_KEY"))
	c.GeminiModel = get("GEMINI_MODEL")
	if c.GeminiModel == "" {
		c.GeminiModel = "gemini-3.8-flash"
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9._-]+$`).MatchString(c.GeminiModel) {
		return c, errors.New("invalid GEMINI_MODEL")
	}
	c.GeminiDailyLimit = 20
	if raw := get("GEMINI_DAILY_REQUEST_LIMIT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 100 {
			return c, errors.New("GEMINI_DAILY_REQUEST_LIMIT must be 0..100")
		}
		c.GeminiDailyLimit = n
	}

	c.GroqKey = strings.TrimSpace(get("GROQ_API_KEY"))
	c.GroqModel = get("GROQ_MODEL")
	if c.GroqModel == "" {
		c.GroqModel = "qwen/qwen3.8-27b"
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`).MatchString(c.GroqModel) {
		return c, errors.New("invalid GROQ_MODEL")
	}
	c.GroqDailyLimit = 20
	if raw := get("GROQ_DAILY_REQUEST_LIMIT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 100 {
			return c, errors.New("GROQ_DAILY_REQUEST_LIMIT must be 0..100")
		}
		c.GroqDailyLimit = n
	}
	c.TMDBToken = strings.TrimSpace(get("TMDB_API_TOKEN"))
	if strings.ContainsAny(c.TMDBToken, "\r\n") || len(c.TMDBToken) > 4096 {
		return c, errors.New("invalid TMDB_API_TOKEN")
	}

	titles := get("FACT_WIKI_TITLES")
	if titles == "" {
		titles = "Alien (film)|Jurassic Park (film)|The Matrix|Back to the Future|Jaws (film)|Blade Runner|The Terminator|Titanic (1997 film)|The Truman Show|The Grand Budapest Hotel|Mad Max: Fury Road|Who Framed Roger Rabbit|The Thing (1982 film)|Raiders of the Lost Ark|The Princess Bride (film)|Groundhog Day (film)|Ghostbusters|The Fifth Element|Interstellar (film)|Inception"
	}
	seen := make(map[string]bool)
	for title := range strings.SplitSeq(titles, "|") {
		title = strings.TrimSpace(title)
		if title == "" || len(title) > 200 || strings.ContainsAny(title, "\r\n") {
			return c, errors.New("invalid FACT_WIKI_TITLES")
		}
		if !seen[title] {
			c.FactWikiTitles = append(c.FactWikiTitles, title)
			seen[title] = true
		}
	}
	if len(c.FactWikiTitles) > 100 {
		return c, errors.New("FACT_WIKI_TITLES allows at most 100 titles")
	}
	return c, nil
}
