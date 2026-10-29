// Command promohub is the promo monitoring hub: Apify/IFTTT webhooks, rules and the event outbox
// in one process (PROMO_HUB_PLAN.md §5).
//
//	promohub serve        migrate, sync config, serve HTTP and run workers (default)
//	promohub migrate      apply migrations and exit
//	promohub check        validate CONFIG_DIR (apps/accounts/rules.yaml) and exit
//	promohub healthcheck  GET /healthz on LISTEN_ADDR (for the container HEALTHCHECK)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lioilsources/promohub/internal/apify"
	"github.com/lioilsources/promohub/internal/collectors/apifysrc"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/hub"
	"github.com/lioilsources/promohub/internal/ifttt"
	"github.com/lioilsources/promohub/internal/notify"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store"
)

var version = "dev"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "migrate":
		err = store.Migrate(os.Getenv("DATABASE_URL"))
	case "check":
		err = check()
	case "healthcheck":
		err = healthcheck()
	case "version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q (serve|migrate|check|healthcheck|version)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "promohub:", err)
		os.Exit(1)
	}
}

func logger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

func loadConfig(dir string) (*config.Files, *rules.Engine, error) {
	files, err := config.LoadFiles(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, t := range files.Accounts.ApifyTasks {
		if _, ok := apifysrc.Kinds[t.Kind]; !ok {
			return nil, nil, fmt.Errorf("accounts.yaml: task %q has unknown kind %q", t.Name, t.Kind)
		}
	}
	eng, err := rules.New(files.Rules)
	if err != nil {
		return nil, nil, err
	}
	return files, eng, nil
}

func check() error {
	dir := os.Getenv("CONFIG_DIR")
	if dir == "" {
		dir = "."
	}
	files, eng, err := loadConfig(dir)
	if err != nil {
		return err
	}
	fmt.Printf("ok: %d apps, %d accounts, %d apify tasks, %d rules\n",
		len(files.Apps), len(files.Accounts.Accounts), len(files.Accounts.ApifyTasks), len(eng.Rules()))
	return nil
}

func serve() error {
	env, err := config.LoadEnv()
	if err != nil {
		return err
	}
	log := logger(env.LogLevel)
	files, eng, err := loadConfig(env.ConfigDir)
	if err != nil {
		return err
	}
	for _, r := range eng.Rules() {
		for _, t := range r.Targets {
			if strings.HasPrefix(t, "ifttt:") && env.IFTTTKey == "" {
				return fmt.Errorf("rule %q targets %s but IFTTT_KEY is not set", r.Name, t)
			}
			if strings.HasPrefix(t, "telegram:") && env.TelegramBotToken == "" {
				return fmt.Errorf("rule %q targets %s but TELEGRAM_BOT_TOKEN is not set", r.Name, t)
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := store.Migrate(env.DatabaseURL); err != nil {
		return err
	}
	st, err := store.Open(ctx, env.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	h := hub.New(hub.Deps{
		Log: log, Env: env, Files: files, Rules: eng, Store: st,
		Apify:    apify.New(env.ApifyToken),
		IFTTT:    ifttt.NewClient(env.IFTTTKey),
		Telegram: &notify.Telegram{Token: env.TelegramBotToken},
	})
	if err := h.SyncConfig(ctx); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              env.ListenAddr,
		Handler:           h.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
	}
	go h.Run(ctx)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("promohub listening", "addr", env.ListenAddr, "version", version,
		"tasks", len(files.Accounts.ApifyTasks), "rules", len(eng.Rules()))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func healthcheck() error {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 4 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz: HTTP %d", resp.StatusCode)
	}
	return nil
}
