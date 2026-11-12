package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/composer"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/hub"
	"github.com/lioilsources/promohub/internal/sparkbroker"
)

// llmWindow reads the night LLM window settings (LLM_WINDOW=00:15 enables it).
func llmWindow(env config.Env, llm *composer.LLM) (*hub.Window, error) {
	at := os.Getenv("LLM_WINDOW")
	if at == "" {
		return nil, nil
	}
	if _, err := time.Parse("15:04", at); err != nil {
		return nil, fmt.Errorf("LLM_WINDOW must be HH:MM (Prague time), got %q", at)
	}
	if llm == nil {
		return nil, fmt.Errorf("LLM_WINDOW needs LLM_BASE_URL and LLM_MODEL")
	}
	base := os.Getenv("SPARK_BROKER")
	if base == "" {
		return nil, fmt.Errorf("LLM_WINDOW needs SPARK_BROKER")
	}
	prio, err := strconv.Atoi(getenvDefault("SPARK_PRIORITY", "15"))
	if err != nil {
		return nil, fmt.Errorf("SPARK_PRIORITY: %w", err)
	}
	return &hub.Window{
		Broker:  &sparkbroker.Client{Base: strings.TrimSuffix(base, "/"), Token: os.Getenv("SPARK_BROKER_TOKEN")},
		At:      at,
		Project: getenvDefault("SPARK_PROJECT", "promohub"), Priority: prio,
		Services: strings.Split(getenvDefault("SPARK_SERVICES", "qwen36"), ","),
		LeaseMin: 20, GrantWait: 60 * time.Minute, Poll: 30 * time.Second, Heartbeat: 2 * time.Minute,
		Retries: 3, RetryWait: 30 * time.Second,
	}, nil
}
