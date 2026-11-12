// Package creds resolves the secrets an adapter needs. Static secrets live in the environment
// under the account's auth_ref prefix (BLUESKY_MAIN + "_APP_PASSWORD"); they never appear in YAML.
package creds

import (
	"fmt"
	"os"
	"strings"

	"github.com/lioilsources/promohub/internal/config"
)

// OAuth is the auth_ref value of accounts whose tokens live encrypted in the credentials table.
const OAuth = "oauth"

// Prefix is the env prefix of an account's static secrets: its auth_ref, or the upper-cased
// platform name when auth_ref is empty.
func Prefix(a config.Account) string {
	if a.AuthRef != "" && a.AuthRef != OAuth {
		return a.AuthRef
	}
	return strings.ToUpper(a.Platform)
}

// Name is the env variable holding one static secret of an account.
func Name(a config.Account, suffix string) string { return Prefix(a) + "_" + suffix }

// Getenv is replaceable in tests.
var Getenv = os.Getenv

// Static returns a static secret, or a MissingError naming the variable to set.
func Static(a config.Account, suffix string) (string, error) {
	n := Name(a, suffix)
	if v := strings.TrimSpace(Getenv(n)); v != "" {
		return v, nil
	}
	return "", &MissingError{Var: n}
}

// MissingError means the account is configured but its secret is not set yet. The scheduler
// treats it as "not connected" rather than as a failing collector.
type MissingError struct{ Var string }

func (e *MissingError) Error() string { return fmt.Sprintf("%s is not set", e.Var) }
