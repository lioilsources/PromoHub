// Package vault encrypts OAuth tokens at rest with AES-256-GCM (Prompts/00 §12). The key is
// PROMOHUB_SECRET_KEY (32 bytes, hex or base64); the associated data binds a ciphertext to
// its account, so rows cannot be swapped between accounts.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

type Vault struct {
	aead cipher.AEAD
	mac  []byte // HMAC key for signed OAuth state, derived from the same secret
}

// New parses a 32-byte key given as 64 hex characters or base64.
func New(key string) (*Vault, error) {
	key = strings.TrimSpace(key)
	var raw []byte
	if b, err := hex.DecodeString(key); err == nil {
		raw = b
	} else if b, err := base64.StdEncoding.DecodeString(key); err == nil {
		raw = b
	} else if b, err := base64.RawURLEncoding.DecodeString(key); err == nil {
		raw = b
	}
	if len(raw) != 32 {
		return nil, errors.New("PROMOHUB_SECRET_KEY must be 32 bytes (openssl rand -hex 32)")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	m := hmac.New(sha256.New, raw)
	m.Write([]byte("promohub oauth state"))
	return &Vault{aead: aead, mac: m.Sum(nil)}, nil
}

// Seal encrypts plaintext for one account.
func (v *Vault) Seal(plaintext []byte, accountID int64) (ciphertext, nonce []byte) {
	nonce = make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return v.aead.Seal(nil, nonce, plaintext, aad(accountID)), nonce
}

// Open decrypts what Seal produced for the same account.
func (v *Vault) Open(ciphertext, nonce []byte, accountID int64) ([]byte, error) {
	b, err := v.aead.Open(nil, nonce, ciphertext, aad(accountID))
	if err != nil {
		return nil, fmt.Errorf("vault: cannot decrypt credentials of account %d (wrong PROMOHUB_SECRET_KEY?)", accountID)
	}
	return b, nil
}

// Sign returns an HMAC-SHA256 of msg, base64url.
func (v *Vault) Sign(msg string) string {
	m := hmac.New(sha256.New, v.mac)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Verify checks a signature from Sign in constant time.
func (v *Vault) Verify(msg, sig string) bool {
	return hmac.Equal([]byte(v.Sign(msg)), []byte(sig))
}

func aad(accountID int64) []byte { return []byte(fmt.Sprintf("promohub/account/%d", accountID)) }
