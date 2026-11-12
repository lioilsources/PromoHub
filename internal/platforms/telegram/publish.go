package telegram

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
	"github.com/lioilsources/promohub/internal/publish"
)

const captionMax = 1024

// Publisher posts to my channel through the hub's bot.
type Publisher struct{ *Adapter }

func (p Publisher) Ready(acct config.Account) error { return p.Adapter.Ready(acct, "") }

type message struct {
	OK     bool   `json:"ok"`
	Desc   string `json:"description"`
	Result struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

func (p Publisher) Publish(ctx context.Context, acct config.Account, post publish.Post) (publish.Result, error) {
	tok, err := token()
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	base := p.Base
	if base == "" {
		base = BaseURL
	}
	c := &httpx.Client{Service: "telegram", BaseURL: base + "/bot" + tok, HTTP: p.HTTP, Secrets: []string{tok}}
	chat := ChatID(acct)
	var img *publish.Media
	for i := range post.Media {
		if post.Media[i].IsImage() || post.Media[i].IsVideo() {
			img = &post.Media[i]
			break
		}
	}
	var res message
	text := post.Text
	if img != nil {
		caption := ""
		if utf8.RuneCountInString(text) <= captionMax {
			caption, text = text, ""
		}
		if err := sendFile(ctx, c, chat, *img, caption, &res); err != nil {
			return publish.Result{}, publish.Reject(err)
		}
	}
	if text != "" {
		err := c.Post(ctx, "/sendMessage", map[string]any{"chat_id": chat, "text": text}, &res)
		if err != nil {
			return publish.Result{}, err
		}
	}
	if !res.OK {
		return publish.Result{}, publish.Reject(fmt.Errorf("telegram: %s", res.Desc))
	}
	id := strconv.FormatInt(res.Result.MessageID, 10)
	u := ""
	if name := strings.TrimPrefix(chat, "@"); name != chat {
		u = "https://t.me/" + name + "/" + id
	}
	return publish.Result{ExternalID: id, URL: u}, nil
}

// sendFile uploads a photo (sendPhoto) or a video (sendVideo, bots may send up to 50 MB).
func sendFile(ctx context.Context, c *httpx.Client, chat string, m publish.Media, caption string, out *message) error {
	method, field := "/sendPhoto", "photo"
	if m.IsVideo() {
		method, field = "/sendVideo", "video"
	}
	f, err := os.Open(m.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	go func() {
		err := w.WriteField("chat_id", chat)
		if err == nil && caption != "" {
			err = w.WriteField("caption", caption)
		}
		if err == nil && m.IsVideo() {
			err = w.WriteField("supports_streaming", "true")
		}
		var part io.Writer
		if err == nil {
			part, err = w.CreateFormFile(field, filepath.Base(m.Path))
		}
		if err == nil {
			_, err = io.Copy(part, f)
		}
		if err == nil {
			err = w.Close()
		}
		pw.CloseWithError(err)
	}()
	up := *c
	up.Header = http.Header{"Content-Type": {w.FormDataContentType()}}
	return up.Do(ctx, http.MethodPost, method, nil, pr, out)
}
