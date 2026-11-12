// Command promohub is the promo monitoring hub: Apify/IFTTT webhooks, rules and the event outbox
// in one process (PROMO_HUB_PLAN.md §5).
//
//	promohub serve        migrate, sync config, serve HTTP and run workers (default)
//	promohub migrate      apply migrations and exit
//	promohub check        validate CONFIG_DIR (apps/accounts/rules.yaml) and exit
//	promohub accounts     table of accounts, read/publish paths and collector state
//	promohub probe a:b    run one read adapter pass against the real API, print JSON, no DB
//	promohub connect a:b  print the OAuth link for an account (needs PROMOHUB_SECRET_KEY)
//	promohub media import --name f.mp4 < f.mp4   store a file for /post media=ID
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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lioilsources/promohub/internal/apify"
	"github.com/lioilsources/promohub/internal/collectors/apifysrc"
	"github.com/lioilsources/promohub/internal/composer"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/hub"
	"github.com/lioilsources/promohub/internal/ifttt"
	"github.com/lioilsources/promohub/internal/notify"
	"github.com/lioilsources/promohub/internal/oauth"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store"
	"github.com/lioilsources/promohub/internal/tgbot"
	"github.com/lioilsources/promohub/internal/vault"
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
	case "accounts":
		err = accounts()
	case "probe":
		err = probe(os.Args[2:])
	case "connect":
		err = connect(os.Args[2:])
	case "media":
		err = mediaCmd(os.Args[2:])
	case "version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q (serve|migrate|check|accounts|probe|connect|media|healthcheck|version)", cmd)
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

	admins, err := tgbot.ParseIDs(env.TelegramAdminIDs)
	if err != nil {
		return err
	}
	var bot *tgbot.Client
	if len(admins) > 0 {
		if env.TelegramBotToken == "" {
			return fmt.Errorf("TELEGRAM_ADMIN_IDS is set but TELEGRAM_BOT_TOKEN is not")
		}
		bot = &tgbot.Client{Token: env.TelegramBotToken}
	}
	var llm *composer.LLM
	if env.LLMBaseURL != "" {
		voice, _ := os.ReadFile(filepath.Join(env.ConfigDir, "voice.md"))
		llm = &composer.LLM{BaseURL: env.LLMBaseURL, Model: env.LLMModel, Key: env.LLMKey, Voice: string(voice)}
	}
	hc := &http.Client{Timeout: 60 * time.Second}
	window, err := llmWindow(env, llm)
	if err != nil {
		return err
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
	var oauthStore *oauth.Store
	var tokens creds.TokenSource
	if key := os.Getenv("PROMOHUB_SECRET_KEY"); key != "" {
		v, err := vault.New(key)
		if err != nil {
			return err
		}
		oauthStore = &oauth.Store{Q: st.Queries, Vault: v, Clients: oauthClients(env.PublicBaseURL, hc)}
		tokens = oauthStore
	} else {
		log.Warn("PROMOHUB_SECRET_KEY not set: OAuth accounts (Instagram, Threads, Facebook, …) stay disconnected")
	}

	h := hub.New(hub.Deps{
		Log: log, Env: env, Files: files, Rules: eng, Store: st,
		Apify:      apify.New(env.ApifyToken),
		IFTTT:      ifttt.NewClient(env.IFTTTKey),
		Telegram:   &notify.Telegram{Token: env.TelegramBotToken},
		Collectors: readAdapters(hc, tokens),
		Publishers: publishers(hc, tokens),
		OAuth:      oauthStore,
		Window:     window,
		Bot:        bot,
		Admins:     admins,
		LLM:        llm,
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
