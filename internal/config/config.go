// Package config loads the hub's YAML files (apps.yaml, accounts.yaml, rules.yaml)
// and process settings from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Env holds process settings. Secrets come only from the environment, never from YAML.
type Env struct {
	ListenAddr         string
	DatabaseURL        string
	ConfigDir          string
	ApifyToken         string
	ApifyWebhookSecret string
	IFTTTKey           string // outbound Webhooks key (maker.ifttt.com)
	IFTTTInboundSecret string // shared secret IFTTT applets put into ?secret=
	TelegramBotToken   string
	LogLevel           string
	EventMaxAttempts   int
	RunMaxAttempts     int
	WorkerInterval     time.Duration
}

func LoadEnv() (Env, error) {
	e := Env{
		ListenAddr:         getenv("LISTEN_ADDR", ":8080"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		ConfigDir:          getenv("CONFIG_DIR", "/config"),
		ApifyToken:         os.Getenv("APIFY_TOKEN"),
		ApifyWebhookSecret: os.Getenv("APIFY_WEBHOOK_SECRET"),
		IFTTTKey:           os.Getenv("IFTTT_KEY"),
		IFTTTInboundSecret: os.Getenv("IFTTT_INBOUND_SECRET"),
		TelegramBotToken:   os.Getenv("TELEGRAM_BOT_TOKEN"),
		LogLevel:           getenv("LOG_LEVEL", "info"),
		EventMaxAttempts:   8,
		RunMaxAttempts:     5,
		WorkerInterval:     15 * time.Second,
	}
	var missing []string
	for name, v := range map[string]string{
		"DATABASE_URL":         e.DatabaseURL,
		"APIFY_TOKEN":          e.ApifyToken,
		"APIFY_WEBHOOK_SECRET": e.ApifyWebhookSecret,
		"IFTTT_INBOUND_SECRET": e.IFTTTInboundSecret,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return e, fmt.Errorf("missing env: %s", strings.Join(missing, ", "))
	}
	// Short secrets in a query string are guessable; insist on something reasonable.
	for name, v := range map[string]string{"APIFY_WEBHOOK_SECRET": e.ApifyWebhookSecret, "IFTTT_INBOUND_SECRET": e.IFTTTInboundSecret} {
		if len(v) < 24 {
			return e, fmt.Errorf("%s must be at least 24 characters", name)
		}
	}
	return e, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// App is one entry of apps.yaml.
type App struct {
	Slug        string            `yaml:"slug"`
	Name        string            `yaml:"name"`
	AppStoreID  string            `yaml:"appstore_id"`
	PlayPackage string            `yaml:"play_package"`
	Keywords    []string          `yaml:"keywords"` // extra words that attribute a mention to this app
	Links       map[string]string `yaml:"links"`
}

// Account is one of my own promo accounts.
type Account struct {
	Platform   string `yaml:"platform"`
	Handle     string `yaml:"handle"`
	ExternalID string `yaml:"external_id"`
	AuthRef    string `yaml:"auth_ref"`
}

// Key is the "platform:handle" form used to reference an account from a task.
func (a Account) Key() string { return a.Platform + ":" + a.Handle }

// ApifyTask is a whitelisted Apify task. Webhooks for any other task are rejected.
type ApifyTask struct {
	Name     string `yaml:"name"`    // stable name, used by IFTTT run_task and in logs
	ID       string `yaml:"id"`      // Apify task ID (actorTaskId in the webhook payload)
	Kind     string `yaml:"kind"`    // dataset mapping, see apifysrc.Kinds
	Account  string `yaml:"account"` // "platform:handle" of the owner account (profile kinds)
	MaxItems int64  `yaml:"max_items"`
}

type Accounts struct {
	Accounts   []Account   `yaml:"accounts"`
	ApifyTasks []ApifyTask `yaml:"apify_tasks"`
}

// RuleSpec is a raw rule from rules.yaml; package rules compiles it.
type RuleSpec struct {
	Name string    `yaml:"name"`
	On   string    `yaml:"on"`
	When string    `yaml:"when"`
	Then StringSet `yaml:"then"`
}

// Sinks maps target names used in rules ("telegram:urgent") to concrete destinations.
type Sinks struct {
	Telegram map[string]string `yaml:"telegram"` // name → chat ID
	Discord  map[string]string `yaml:"discord"`  // name → webhook URL
}

type Rules struct {
	Rules []RuleSpec `yaml:"rules"`
	Sinks Sinks      `yaml:"sinks"`
}

// StringSet accepts either a scalar or a list in YAML.
type StringSet []string

func (s *StringSet) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*s = StringSet{n.Value}
		return nil
	case yaml.SequenceNode:
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		*s = l
		return nil
	}
	return fmt.Errorf("line %d: expected string or list", n.Line)
}

// Files is everything loaded from CONFIG_DIR.
type Files struct {
	Apps     []App
	Accounts Accounts
	Rules    Rules
}

func LoadFiles(dir string) (*Files, error) {
	f := &Files{}
	var apps struct {
		Apps []App `yaml:"apps"`
	}
	if err := loadYAML(filepath.Join(dir, "apps.yaml"), &apps, true); err != nil {
		return nil, err
	}
	f.Apps = apps.Apps
	if err := loadYAML(filepath.Join(dir, "accounts.yaml"), &f.Accounts, false); err != nil {
		return nil, err
	}
	if err := loadYAML(filepath.Join(dir, "rules.yaml"), &f.Rules, false); err != nil {
		return nil, err
	}
	return f, f.validate()
}

// loadYAML expands ${VAR} from the environment so webhook URLs and chat IDs can stay in .env.
func loadYAML(path string, out any, optional bool) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && optional {
		return nil
	}
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(b))), out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (f *Files) validate() error {
	accts := map[string]bool{}
	for _, a := range f.Accounts.Accounts {
		if a.Platform == "" || a.Handle == "" {
			return fmt.Errorf("accounts.yaml: account needs platform and handle")
		}
		accts[a.Key()] = true
	}
	names, ids := map[string]bool{}, map[string]bool{}
	for _, t := range f.Accounts.ApifyTasks {
		switch {
		case t.Name == "" || t.ID == "" || t.Kind == "":
			return fmt.Errorf("accounts.yaml: apify task needs name, id and kind")
		case names[t.Name] || ids[t.ID]:
			return fmt.Errorf("accounts.yaml: duplicate apify task %q", t.Name)
		case t.Account != "" && !accts[t.Account]:
			return fmt.Errorf("accounts.yaml: task %q references unknown account %q", t.Name, t.Account)
		}
		names[t.Name], ids[t.ID] = true, true
	}
	slugs := map[string]bool{}
	for _, a := range f.Apps {
		if a.Slug == "" || a.Name == "" {
			return fmt.Errorf("apps.yaml: app needs slug and name")
		}
		if slugs[a.Slug] {
			return fmt.Errorf("apps.yaml: duplicate slug %q", a.Slug)
		}
		slugs[a.Slug] = true
	}
	return nil
}

// TaskByID returns the whitelisted task with this Apify ID.
func (f *Files) TaskByID(id string) (ApifyTask, bool) {
	for _, t := range f.Accounts.ApifyTasks {
		if t.ID == id {
			return t, true
		}
	}
	return ApifyTask{}, false
}

// TaskByName returns the whitelisted task with this name.
func (f *Files) TaskByName(name string) (ApifyTask, bool) {
	for _, t := range f.Accounts.ApifyTasks {
		if t.Name == name {
			return t, true
		}
	}
	return ApifyTask{}, false
}

// Account returns the account for a "platform:handle" key.
func (f *Files) Account(key string) (Account, bool) {
	for _, a := range f.Accounts.Accounts {
		if a.Key() == key {
			return a, true
		}
	}
	return Account{}, false
}

// AppFor attributes free text to an app by name, slug or keyword (case-insensitive).
func (f *Files) AppFor(text string) (App, bool) {
	t := strings.ToLower(text)
	for _, a := range f.Apps {
		for _, w := range append([]string{a.Name, a.Slug}, a.Keywords...) {
			if w != "" && strings.Contains(t, strings.ToLower(w)) {
				return a, true
			}
		}
	}
	return App{}, false
}
