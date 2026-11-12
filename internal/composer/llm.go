package composer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LLM rewrites the campaign body per account through an OpenAI-compatible chat endpoint
// (LLM_BASE_URL, e.g. the LiteLLM gateway). Only my own campaign text goes into the prompt,
// never scraped comments or mentions.
type LLM struct {
	BaseURL string // …/v1
	Model   string
	Key     string
	Voice   string // style notes (voice.md in CONFIG_DIR)
	HTTP    *http.Client
}

// Target is one account the LLM writes for.
type Target struct {
	Key      string `json:"account"`
	Platform string `json:"platform"`
	Limit    int    `json:"max_chars"`
	Lang     string `json:"language"`
}

// Rewrite returns account key → body text. The caller still runs Compose on each body, so
// links, hashtags and the length limit are enforced whatever the model returns.
func (l *LLM) Rewrite(ctx context.Context, c Campaign, targets []Target) (map[string]string, error) {
	if l == nil || l.BaseURL == "" || l.Model == "" {
		return nil, errors.New("llm: not configured")
	}
	in, _ := json.Marshal(map[string]any{
		"app": appName(c), "title": c.Title, "message": c.Body, "has_link": c.Link != "", "targets": targets,
	})
	sys := "You adapt one announcement of an indie app developer to several social accounts. " +
		"Keep every fact, invent nothing, no hashtags, no links (they are added later), no emoji spam. " +
		"Write in the target's language. Stay well under max_chars (links and hashtags need room). " +
		"Answer with a JSON object mapping each target's account to its text, nothing else."
	if l.Voice != "" {
		sys += "\n\nVoice and style:\n" + l.Voice
	}
	req := map[string]any{
		"model": l.Model,
		"messages": []map[string]string{
			{"role": "system", "content": sys},
			{"role": "user", "content": string(in)},
		},
		"temperature":     0.4,
		"response_format": map[string]string{"type": "json_object"},
	}
	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := l.post(ctx, req, &res); err != nil {
		return nil, err
	}
	if len(res.Choices) == 0 {
		return nil, errors.New("llm: empty answer")
	}
	out := map[string]string{}
	content := strings.TrimSpace(res.Choices[0].Message.Content)
	content = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(content, "```json"), "```"), "```")
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("llm: answer is not a JSON object: %w", err)
	}
	return out, nil
}

func (l *LLM) post(ctx context.Context, body, out any) error {
	b, _ := json.Marshal(body)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(l.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	if l.Key != "" {
		r.Header.Set("Authorization", "Bearer "+l.Key)
	}
	hc := l.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 70 * time.Second}
	}
	resp, err := hc.Do(r)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("llm: HTTP %d: %.200s", resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}

func appName(c Campaign) string {
	if c.App != nil {
		return c.App.Name
	}
	return ""
}
