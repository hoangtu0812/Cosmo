// Package secrets encrypts values that must survive in the database but never
// be readable from it — today the per-workspace model gateway API keys.
//
// The key is derived from SESSION_SECRET with SHA-256 rather than configured
// separately, so an installation gains encryption without managing a second
// secret. The trade-off is deliberate and worth knowing: rotating
// SESSION_SECRET makes every stored key undecryptable, and each workspace has
// to re-enter its API key.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
)

// ErrNotConfigured is returned when the box was built without a secret.
var ErrNotConfigured = errors.New("secret key is not configured")

// Box seals and opens short secrets with AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// New derives an AES-256-GCM box from the session secret. An empty secret
// yields a box that refuses to seal or open, so callers surface a clear error
// instead of silently storing plaintext.
func New(sessionSecret string) (*Box, error) {
	if sessionSecret == "" {
		return &Box{}, nil
	}
	key := sha256.Sum256([]byte(sessionSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Configured reports whether the box can seal and open values.
func (b *Box) Configured() bool { return b != nil && b.aead != nil }

// Seal encrypts plaintext, returning nonce||ciphertext.
func (b *Box) Seal(plaintext string) ([]byte, error) {
	if !b.Configured() {
		return nil, ErrNotConfigured
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(sealed []byte) (string, error) {
	if !b.Configured() {
		return "", ErrNotConfigured
	}
	if len(sealed) < b.aead.NonceSize() {
		return "", errors.New("sealed value is too short")
	}
	nonce, ciphertext := sealed[:b.aead.NonceSize()], sealed[b.aead.NonceSize():]
	plaintext, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

// Hint renders the last four characters of a secret for display, so an
// operator can tell which key is stored without the key ever leaving the
// server intact.
func Hint(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return "••••"
	}
	return "••••" + secret[len(secret)-4:]
}
