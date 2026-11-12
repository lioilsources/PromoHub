package mastodon

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"time"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
	"github.com/lioilsources/promohub/internal/publish"
)

// Publisher adapts the Mastodon adapter to publish.Publisher.
type Publisher struct{ *Adapter }

func (p Publisher) Ready(acct config.Account) error {
	_, err := creds.Static(acct, SecretSuffix)
	return err
}

// Publish posts a public status. The attempt key goes out as Idempotency-Key, so a retry of
// the same attempt cannot create a second status.
func (p Publisher) Publish(ctx context.Context, acct config.Account, post publish.Post) (publish.Result, error) {
	if err := p.Ready(acct); err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	c := p.client(acct)
	var ids []string
	for i, m := range post.Media {
		if i == 4 {
			break
		}
		id, err := p.upload(ctx, c, m)
		if err != nil {
			return publish.Result{}, publish.Reject(fmt.Errorf("media %s: %w", filepath.Base(m.Path), err))
		}
		ids = append(ids, id)
	}
	sc := *c
	sc.Header = c.Header.Clone()
	if post.AttemptKey != "" {
		sc.Header.Set("Idempotency-Key", post.AttemptKey)
	}
	body := map[string]any{"status": post.Text, "visibility": "public"}
	if len(ids) > 0 {
		body["media_ids"] = ids
	}
	if post.Lang != "" {
		body["language"] = post.Lang
	}
	var st Status
	if err := sc.Post(ctx, "/api/v1/statuses", body, &st); err != nil {
		return publish.Result{}, err
	}
	return publish.Result{ExternalID: st.ID, URL: st.URL}, nil
}

type media struct {
	ID  string  `json:"id"`
	URL *string `json:"url"` // null while the server still processes it
}

// upload sends one file to /api/v2/media and waits (up to ~1 min) until it is processed.
func (p Publisher) upload(ctx context.Context, c *httpx.Client, m publish.Media) (string, error) {
	f, err := os.Open(m.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// Stream the multipart body: videos do not fit the container's memory limit.
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	go func() {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filepath.Base(m.Path)))
		h.Set("Content-Type", m.Mime)
		part, err := w.CreatePart(h)
		if err == nil {
			_, err = io.Copy(part, f)
		}
		if err == nil && m.Alt != "" {
			err = w.WriteField("description", m.Alt)
		}
		if err == nil {
			err = w.Close()
		}
		pw.CloseWithError(err)
	}()
	up := *c
	up.Header = c.Header.Clone()
	up.Header.Set("Content-Type", w.FormDataContentType())
	var res media
	if err := up.Do(ctx, http.MethodPost, "/api/v2/media", nil, pr, &res); err != nil {
		pr.CloseWithError(err)
		return "", err
	}
	for i := 0; res.URL == nil && i < 90; i++ { // videos take a while
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(p.pollEvery()):
		}
		if err := c.Get(ctx, "/api/v1/media/"+res.ID, nil, &res); err != nil && httpx.Code(err) != http.StatusPartialContent {
			return "", err
		}
	}
	if res.URL == nil {
		return "", fmt.Errorf("media %s still processing", res.ID)
	}
	return res.ID, nil
}

func (p Publisher) pollEvery() time.Duration {
	if p.PollEvery > 0 {
		return p.PollEvery
	}
	return 2 * time.Second
}
