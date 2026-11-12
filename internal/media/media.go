// Package media stores uploaded files on disk for /media/{token}. Platforms that pull media
// from a URL (Instagram, Threads, IFTTT) need it public; the token is the only key.
package media

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register decoders for DecodeConfig
	_ "image/jpeg" //
	_ "image/png"  //
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MaxBytes caps one file (video included); the hub does no transcoding.
const MaxBytes = 300 << 20

// Saved describes a stored file.
type Saved struct {
	Path       string
	Name       string
	Mime       string
	Bytes      int64
	Sha256     string
	Width      int
	Height     int
	DurationMs int // videos
}

// Save copies r into dir under a content-addressed name.
func Save(dir string, r io.Reader, name string) (Saved, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Saved{}, err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return Saved{}, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	var head bytes.Buffer
	n, err := io.Copy(io.MultiWriter(tmp, h, &limitBuf{&head, 512 << 10}), io.LimitReader(r, MaxBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Saved{}, err
	}
	if n > MaxBytes {
		return Saved{}, fmt.Errorf("file is larger than %d MB", MaxBytes>>20)
	}
	if n == 0 {
		return Saved{}, errors.New("empty file")
	}
	s := Saved{Name: filepath.Base(name), Bytes: n, Sha256: hex.EncodeToString(h.Sum(nil))}
	s.Mime = mimeOf(name, head.Bytes())
	if strings.HasPrefix(s.Mime, "image/") {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(head.Bytes())); err == nil {
			s.Width, s.Height = cfg.Width, cfg.Height
		}
	}
	ext := filepath.Ext(name)
	if ext == "" {
		if exts, _ := mime.ExtensionsByType(s.Mime); len(exts) > 0 {
			ext = exts[0]
		}
	}
	s.Path = filepath.Join(dir, s.Sha256[:32]+strings.ToLower(ext))
	if err := os.Rename(tmp.Name(), s.Path); err != nil {
		return Saved{}, err
	}
	if strings.HasPrefix(s.Mime, "video/") {
		vi, err := ProbeMP4(s.Path)
		if err != nil {
			os.Remove(s.Path)
			return Saved{}, fmt.Errorf("video: %w (MP4 or MOV only, no transcoding)", err)
		}
		s.Width, s.Height, s.DurationMs = vi.Width, vi.Height, vi.DurationMs
	}
	return s, nil
}

func mimeOf(name string, head []byte) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		t, _, _ = strings.Cut(t, ";")
		return t
	}
	t, _, _ := strings.Cut(http.DetectContentType(head), ";")
	return t
}

// Token is an unguessable URL key (32 random bytes).
func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// limitBuf keeps the first max bytes of a stream (for type sniffing and image headers).
type limitBuf struct {
	b   *bytes.Buffer
	max int
}

func (l *limitBuf) Write(p []byte) (int, error) {
	if room := l.max - l.b.Len(); room > 0 {
		l.b.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}
