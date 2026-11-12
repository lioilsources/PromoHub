// Package tgbot is the slice of the Telegram Bot API the approval bot uses: long polling,
// messages with inline keyboards, edits, ForceReply prompts and file downloads.
package tgbot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

const BaseURL = "https://api.telegram.org"

// MaxDownload is the Bot API limit for getFile downloads.
const MaxDownload = 20 << 20

type Client struct {
	Token string
	Base  string
	HTTP  *http.Client
}

func (c *Client) api() *httpx.Client {
	base := c.Base
	if base == "" {
		base = BaseURL
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 70 * time.Second} // above the long-poll timeout
	}
	return &httpx.Client{Service: "telegram", BaseURL: base + "/bot" + c.Token, HTTP: hc, Secrets: []string{c.Token}}
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type PhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type Message struct {
	MessageID      int64       `json:"message_id"`
	From           *User       `json:"from"`
	Chat           Chat        `json:"chat"`
	Text           string      `json:"text"`
	Caption        string      `json:"caption"`
	Photo          []PhotoSize `json:"photo"`
	Video          *Document   `json:"video"`
	Document       *Document   `json:"document"`
	ReplyToMessage *Message    `json:"reply_to_message"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Button is an inline keyboard button: either callback Data or a URL.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Keyboard is rows of buttons.
type Keyboard [][]Button

type result[T any] struct {
	OK     bool   `json:"ok"`
	Desc   string `json:"description"`
	Result T      `json:"result"`
}

func call[T any](ctx context.Context, c *Client, method string, body any) (T, error) {
	var r result[T]
	if err := c.api().Post(ctx, "/"+method, body, &r); err != nil {
		return r.Result, err
	}
	if !r.OK {
		return r.Result, fmt.Errorf("telegram %s: %s", method, r.Desc)
	}
	return r.Result, nil
}

// Updates long-polls for new updates after offset.
func (c *Client) Updates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	return call[[]Update](ctx, c, "getUpdates", map[string]any{
		"offset": offset, "timeout": int(timeout.Seconds()), "allowed_updates": []string{"message", "callback_query"},
	})
}

// Opts are optional fields of a sent message.
type Opts struct {
	Keyboard    Keyboard
	ForceReply  bool   // the next reply from Olin answers this message
	Placeholder string // ForceReply input placeholder
	HTML        bool   // parse_mode HTML; only for text the hub built and escaped itself
	ReplyTo     int64
}

func (o Opts) apply(m map[string]any) {
	switch {
	case o.ForceReply:
		fr := map[string]any{"force_reply": true, "selective": true}
		if o.Placeholder != "" {
			fr["input_field_placeholder"] = o.Placeholder
		}
		m["reply_markup"] = fr
	case o.Keyboard != nil:
		m["reply_markup"] = map[string]any{"inline_keyboard": o.Keyboard}
	}
	if o.HTML {
		m["parse_mode"] = "HTML"
	}
	if o.ReplyTo != 0 {
		m["reply_parameters"] = map[string]any{"message_id": o.ReplyTo, "allow_sending_without_reply": true}
	}
	m["link_preview_options"] = map[string]any{"is_disabled": true}
}

// Send sends a text message and returns its ID. Text is cut to the 4096-character limit.
func (c *Client) Send(ctx context.Context, chat int64, text string, o Opts) (int64, error) {
	m := map[string]any{"chat_id": chat, "text": cut(text, 4096)}
	o.apply(m)
	msg, err := call[Message](ctx, c, "sendMessage", m)
	return msg.MessageID, err
}

// Edit replaces a message's text and keyboard (nil keyboard removes it).
func (c *Client) Edit(ctx context.Context, chat, msgID int64, text string, o Opts) error {
	m := map[string]any{"chat_id": chat, "message_id": msgID, "text": cut(text, 4096)}
	if o.Keyboard == nil {
		o.Keyboard = Keyboard{}
	}
	o.apply(m)
	_, err := call[any](ctx, c, "editMessageText", m)
	return err
}

// Answer acknowledges a button press (the spinner on the button stops).
func (c *Client) Answer(ctx context.Context, callbackID, text string) error {
	_, err := call[bool](ctx, c, "answerCallbackQuery", map[string]any{"callback_query_id": callbackID, "text": text})
	return err
}

// Download fetches a file the user sent (at most MaxDownload bytes).
func (c *Client) Download(ctx context.Context, fileID string) (io.ReadCloser, error) {
	f, err := call[struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}](ctx, c, "getFile", map[string]any{"file_id": fileID})
	if err != nil {
		return nil, err
	}
	if f.FileSize > MaxDownload {
		return nil, fmt.Errorf("file is %d MB; the Bot API downloads at most 20 MB (use make media)", f.FileSize>>20)
	}
	base := c.Base
	if base == "" {
		base = BaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/file/bot"+c.Token+"/"+(&url.URL{Path: f.FilePath}).EscapedPath(), nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: bad file path")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: download failed")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("telegram: download HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// ParseIDs reads TELEGRAM_ADMIN_IDS ("123,456").
func ParseIDs(s string) ([]int64, error) {
	var out []int64
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		id, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("TELEGRAM_ADMIN_IDS: %q is not a numeric user ID", f)
		}
		out = append(out, id)
	}
	return out, nil
}
