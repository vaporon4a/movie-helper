package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

// Exercise the real SDK encoder: parameter fakes miss typed nil interfaces
// becoming reply_markup=null on the wire.
func TestDailySenderReplyMarkupOnWire(t *testing.T) {
	for _, kind := range []string{daily.Meme, daily.Fact} {
		for _, preview := range []bool{false, true} {
			name := kind + "/scheduled"
			if preview {
				name = kind + "/preview"
			}
			t.Run(name, func(t *testing.T) {
				client := &dailyWireClient{}
				api, err := bot.New("test-token", bot.WithSkipGetMe(), bot.WithHTTPClient(time.Second, client))
				if err != nil {
					t.Fatal(err)
				}
				sender := Sender{API: api, Closable: preview}
				id, err := sender.Send(context.Background(), -1, daily.Item{Kind: kind, Text: "Материал", Image: "https://example.org/meme.jpg"})
				if err != nil || id != 99 {
					t.Fatalf("send: id=%d err=%v", id, err)
				}
				if !preview && client.markupPresent {
					t.Fatalf("scheduled publication must omit reply_markup, got %q", client.markup)
				}
				if preview {
					var markup struct {
						Keyboard [][]json.RawMessage `json:"inline_keyboard"`
					}
					if err := json.Unmarshal([]byte(client.markup), &markup); err != nil || len(markup.Keyboard) == 0 {
						t.Fatalf("preview needs a close keyboard, got %q: %v", client.markup, err)
					}
				}
			})
		}
	}
}

type dailyWireClient struct {
	markup        string
	markupPresent bool
}

func (c *dailyWireClient) Do(req *http.Request) (*http.Response, error) {
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		return nil, err
	}
	defer req.MultipartForm.RemoveAll()
	c.markup = req.FormValue("reply_markup")
	_, c.markupPresent = req.MultipartForm.Value["reply_markup"]
	body := `{"ok":true,"result":{"message_id":99}}`
	if c.markupPresent && c.markup == "null" {
		body = `{"ok":false,"error_code":400,"description":"Bad Request: object expected as reply markup"}`
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}
