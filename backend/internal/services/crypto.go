package services

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
)

// storageKeyInfo is the HKDF "info" label that domain-separates the at-rest
// storage key from any other use of the same secret.
const storageKeyInfo = "capstan-token-encryption-v1"

// TokenEncryptor encrypts sensitive settings (git HTTPS token, restic password)
// at rest with AES-256-GCM.
//
// The primary key is derived from a dedicated storage secret via HKDF-SHA256
// (H2): this decouples at-rest encryption from JWT_SECRET so disclosing the JWT
// signing secret alone does not also expose stored secrets, and a low-entropy
// secret is no longer used as an AES key via a single SHA-256 pass.
//
// A legacy AEAD keyed by SHA-256(JWT_SECRET) — the previous scheme — is retained
// for decryption only, so existing ciphertexts remain readable. They are
// re-encrypted with the primary key on the next write.
type TokenEncryptor struct {
	primary cipher.AEAD
	legacy  cipher.AEAD // decrypt-only; nil when no legacy secret is available
}

// NewTokenEncryptor builds an encryptor. storageSecret derives the primary key;
// when it is empty it falls back to jwtSecret (no dedicated STORAGE_KEY set).
// jwtSecret, when non-empty, reconstructs the legacy decryption key.
func NewTokenEncryptor(storageSecret, jwtSecret string) (*TokenEncryptor, error) {
	if storageSecret == "" {
		storageSecret = jwtSecret
	}
	if storageSecret == "" {
		return nil, errors.New("no secret available to derive storage key")
	}

	primaryKey, err := hkdf.Key(sha256.New, []byte(storageSecret), nil, storageKeyInfo, 32)
	if err != nil {
		return nil, err
	}
	primary, err := newGCM(primaryKey)
	if err != nil {
		return nil, err
	}

	enc := &TokenEncryptor{primary: primary}

	// Legacy key: SHA-256(jwtSecret), used only to read pre-HKDF ciphertext.
	if jwtSecret != "" {
		legacyHash := sha256.Sum256([]byte(jwtSecret))
		if legacy, lerr := newGCM(legacyHash[:]); lerr == nil {
			enc.legacy = legacy
		}
	}

	return enc, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encryptor is the interface satisfied by both *TokenEncryptor and the
// null-object noEncryptor below. database.DB depends on this shape
// structurally (it declares its own identical TokenEncryptor interface), so
// returning it here does not require the database package to import services.
type Encryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(encoded string) (string, error)
}

// ErrEncryptionUnavailable is returned by noEncryptor's Encrypt/Decrypt (check
// with errors.Is) when no STORAGE_KEY or JWT_SECRET was configured at
// startup.
//
// Background (agent-os-16m): NewTokenEncryptorOrDefault used to return a nil
// *TokenEncryptor in this case. A nil concrete pointer assigned to an
// interface-typed parameter (database.NewWithMigrationsAndEncryptor's
// `encryptor TokenEncryptor` argument) produces a NON-nil interface value —
// so every "if d.encryptor != nil" guard downstream (database/settings.go,
// database/directories.go) evaluated true anyway, and Encrypt/Decrypt then
// ran on a nil receiver: a panic on the first write of an encryptable setting
// (e.g. PUT /api/v1/settings/backup with a restic password). noEncryptor is a
// genuine non-nil value, so those guards now correctly delegate to it instead
// of being silently bypassed.
var ErrEncryptionUnavailable = errors.New("no encryption key configured: set STORAGE_KEY or JWT_SECRET")

// noEncryptor is the null-object Encryptor returned when construction fails.
// It deliberately does NOT silently pass plaintext through: skipping
// encryption because no key is configured would violate the "never persist
// secrets in plaintext" invariant (L1), so it fails loudly with
// ErrEncryptionUnavailable instead, and callers map that to a clear,
// actionable API error.
type noEncryptor struct{}

func (noEncryptor) Encrypt(string) (string, error) { return "", ErrEncryptionUnavailable }
func (noEncryptor) Decrypt(string) (string, error) { return "", ErrEncryptionUnavailable }

// NewTokenEncryptorOrDefault constructs a TokenEncryptor, returning a
// null-object noEncryptor (with a WARN logged) if construction fails, so the
// caller degrades gracefully instead of receiving a nil pointer.
//
// DECISION (agent-os-16m, recorded here per the tracker's acceptance
// criteria): a missing encryption key is deliberately NOT made fatal at
// startup. AUTH_DISABLED=true is a documented no-config mode, and requiring
// STORAGE_KEY/JWT_SECRET at startup would break that mode outright. The
// tradeoff this accepts: encryptable settings (restic_password,
// git_https_token) become unwritable — with a clear, actionable error
// surfaced at the point of use — until one of those env vars is set. This
// WARN is the only startup-time signal of that; see ErrEncryptionUnavailable
// above for where the gap becomes visible later, and cleanly, instead of as
// a panic.
func NewTokenEncryptorOrDefault(storageSecret, jwtSecret string) Encryptor {
	enc, err := NewTokenEncryptor(storageSecret, jwtSecret)
	if err != nil {
		slog.Warn("Failed to create token encryptor, tokens will not be encrypted at rest", "error", err)
		return noEncryptor{}
	}
	return enc
}

func (e *TokenEncryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	nonce := make([]byte, e.primary.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := e.primary.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (e *TokenEncryptor) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}

	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("invalid encrypted token format")
	}

	if pt, ok := openWith(e.primary, ciphertext); ok {
		return pt, nil
	}
	// Fall back to the legacy key for ciphertext written by the previous scheme.
	if e.legacy != nil {
		if pt, ok := openWith(e.legacy, ciphertext); ok {
			return pt, nil
		}
	}

	return "", errors.New("failed to decrypt token")
}

// openWith attempts to authenticate-and-decrypt ciphertext (nonce-prefixed) with
// aead. It returns ok=false on any error rather than the error itself so the
// caller can try the next key.
func openWith(aead cipher.AEAD, ciphertext []byte) (string, bool) {
	nonceSize := aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", false
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", false
	}
	return string(plaintext), true
}
