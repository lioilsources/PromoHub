package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/hub"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store"
)

// mediaCmd: `promohub media import --name clip.mp4 [--alt "…"] < clip.mp4` stores a file too
// big for the Telegram Bot API (20 MB) and prints the ID to use as media=ID in /post.
func mediaCmd(args []string) error {
	if len(args) == 0 || args[0] != "import" {
		return fmt.Errorf("usage: promohub media import --name <file name> [--alt text] < file")
	}
	fs := flag.NewFlagSet("media import", flag.ContinueOnError)
	name := fs.String("name", "", "file name (its extension decides the type)")
	alt := fs.String("alt", "", "alt text for images")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, url)
	if err != nil {
		return err
	}
	defer st.Close()
	env := config.Env{MediaDir: getenvDefault("MEDIA_DIR", "/data/media"), PublicBaseURL: getenvDefault("PUBLIC_BASE_URL", "https://promo.ol1n.com")}
	eng, _ := rules.New(config.Rules{})
	h := hub.New(hub.Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Env: env, Files: &config.Files{}, Rules: eng, Store: st})
	a, err := h.SaveMedia(ctx, os.Stdin, *name, *alt)
	if err != nil {
		return err
	}
	fmt.Printf("media=%d  %s  %d bytes\n", a.ID, a.Mime, a.Bytes)
	return nil
}

func getenvDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
