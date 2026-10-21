package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectsHTTPErrorOversizedAndMultipleJSONValues(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"status":    {http.StatusTooManyRequests, `{}`},
		"oversized": {http.StatusOK, strings.Repeat(" ", maxResponseBytes+1)},
		"multiple":  {http.StatusOK, `{"id":603,"title":"Матрица","release_date":"1999-03-31"} {}`},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := &Client{HTTP: server.Client(), BaseURL: server.URL, Token: "secret"}
			if _, err := client.FilmIdentity(context.Background(), 603); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}
