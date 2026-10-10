package config

import (
	_ "embed"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
)

//go:embed fact_titles.txt
var defaultFactTitles string

type Config struct {
	GeminiKey, GeminiModel string
	GeminiMemeModel        string
	GeminiDailyLimit       int
	GroqKey, GroqModel     string
	GroqDailyLimit         int
	TMDBToken              string
	FactWikiTitles         []string
	Token, DBPath          string
	Chats                  map[int64]bool
	Subreddits             []string
	AIResultMode           string
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
	for _, read := range []func(func(string) string) error{c.readChats, c.readMemes, c.readRuntime, c.readProviders, c.readTitles} {
		if err := read(get); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (c *Config) readChats(get func(string) string) error {
	chats := strings.TrimSpace(get("ALLOWED_CHAT_IDS"))
	// Explicit onboarding mode: /id and private help work, all groups are denied.
	if chats != "bootstrap" {
		for part := range strings.SplitSeq(chats, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil || id >= 0 {
				return errors.New("ALLOWED_CHAT_IDS must list negative group IDs or be bootstrap")
			}
			c.Chats[id] = true
		}
	}

	return nil
}

func (c *Config) readMemes(get func(string) string) error {
	subs := get("MEME_SUBREDDITS")
	if subs == "" {
		subs = "RUSSIANMemeSub,Pikabu,expectedrussians"
	}
	for sub := range strings.SplitSeq(subs, ",") {
		sub = strings.TrimSpace(sub)
		if !regexp.MustCompile(`^[A-Za-z0-9_]{2,30}$`).MatchString(sub) {
			return errors.New("invalid MEME_SUBREDDITS")
		}
		c.Subreddits = append(c.Subreddits, sub)
	}
	if len(c.Subreddits) > 5 {
		return errors.New("MEME_SUBREDDITS allows at most 5 sources")
	}

	return nil
}

func (c *Config) readRuntime(get func(string) string) error {
	c.AIResultMode = strings.TrimSpace(get("AI_RESULT_MODE"))
	if c.AIResultMode == "" {
		c.AIResultMode = "warmup"
	}
	if c.AIResultMode != "warmup" && c.AIResultMode != "background" {
		return errors.New("AI_RESULT_MODE must be warmup or background")
	}
	if level := get("LOG_LEVEL"); level != "" {
		if err := c.Level.UnmarshalText([]byte(level)); err != nil {
			return errors.New("invalid LOG_LEVEL")
		}
	}

	return nil
}

func (c *Config) readProviders(get func(string) string) error {
	c.GeminiKey = strings.TrimSpace(get("GEMINI_API_KEY"))
	c.GeminiModel = get("GEMINI_MODEL")
	if c.GeminiModel == "" {
		c.GeminiModel = "gemini-3.8-flash"
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9._-]+$`).MatchString(c.GeminiModel) {
		return errors.New("invalid GEMINI_MODEL")
	}
	c.GeminiMemeModel = get("GEMINI_MEME_MODEL")
	if c.GeminiMemeModel == "" {
		c.GeminiMemeModel = "gemini-3.5-flash-lite"
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9._-]+$`).MatchString(c.GeminiMemeModel) {
		return errors.New("invalid GEMINI_MEME_MODEL")
	}
	c.GeminiDailyLimit = 20
	if raw := get("GEMINI_DAILY_REQUEST_LIMIT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 100 {
			return errors.New("GEMINI_DAILY_REQUEST_LIMIT must be 0..100")
		}
		c.GeminiDailyLimit = n
	}

	if err := c.readGroq(get); err != nil {
		return err
	}
	c.TMDBToken = strings.TrimSpace(get("TMDB_API_TOKEN"))
	if strings.ContainsAny(c.TMDBToken, "\r\n") || len(c.TMDBToken) > 4096 {
		return errors.New("invalid TMDB_API_TOKEN")
	}

	return nil
}

func (c *Config) readTitles(get func(string) string) error {
	titles := get("FACT_WIKI_TITLES")
	if titles == "" {
		titles = strings.ReplaceAll(strings.TrimSpace(defaultFactTitles), "\n", "|")
	}
	seen := make(map[string]bool)
	for title := range strings.SplitSeq(titles, "|") {
		title = strings.TrimSpace(title)
		if title == "" || len(title) > 200 || strings.ContainsAny(title, "\r\n") {
			return errors.New("invalid FACT_WIKI_TITLES")
		}
		if !seen[title] {
			c.FactWikiTitles = append(c.FactWikiTitles, title)
			seen[title] = true
		}
	}
	if len(c.FactWikiTitles) > 100 {
		return errors.New("FACT_WIKI_TITLES allows at most 100 titles")
	}

	return nil
}

func (c *Config) readGroq(get func(string) string) error {
	c.GroqKey = strings.TrimSpace(get("GROQ_API_KEY"))
	c.GroqModel = get("GROQ_MODEL")
	if c.GroqModel == "" {
		c.GroqModel = "qwen/qwen3.8-27b"
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`).MatchString(c.GroqModel) {
		return errors.New("invalid GROQ_MODEL")
	}
	c.GroqDailyLimit = 20
	if raw := get("GROQ_DAILY_REQUEST_LIMIT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 100 {
			return errors.New("GROQ_DAILY_REQUEST_LIMIT must be 0..100")
		}
		c.GroqDailyLimit = n
	}
	return nil
}
