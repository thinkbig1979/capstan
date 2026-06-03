package services

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testStorageKey = "storage-key-distinct-from-the-jwt"
	testJWTSecret  = "jwt-secret-value-thirty-two-chars"
)

// legacyEncrypt reproduces the pre-HKDF scheme (AES-256-GCM with key =
// SHA-256(jwtSecret)) so we can assert the new encryptor still decrypts data
// written by the old code.
func legacyEncrypt(t *testing.T, jwtSecret, plaintext string) string {
	t.Helper()
	key := sha256.Sum256([]byte(jwtSecret))
	block, err := aes.NewCipher(key[:])
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, aead.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	require.NoError(t, err)
	ct := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct)
}

func TestTokenEncryptor_RoundTrip(t *testing.T) {
	enc, err := NewTokenEncryptor(testStorageKey, testJWTSecret)
	require.NoError(t, err)

	ct, err := enc.Encrypt("hunter2")
	require.NoError(t, err)
	assert.NotEqual(t, "hunter2", ct)

	pt, err := enc.Decrypt(ct)
	require.NoError(t, err)
	assert.Equal(t, "hunter2", pt)
}

// TestTokenEncryptor_DecryptsLegacyCiphertext is the backward-compat guarantee
// for H2: secrets written under the old SHA-256(JWT_SECRET) key must still
// decrypt after the switch to an HKDF-derived storage key.
func TestTokenEncryptor_DecryptsLegacyCiphertext(t *testing.T) {
	legacyCT := legacyEncrypt(t, testJWTSecret, "old-git-token")

	// STORAGE_KEY unset -> storageSecret falls back to jwtSecret; the legacy
	// SHA-256(jwtSecret) AEAD must still open the old ciphertext.
	enc, err := NewTokenEncryptor("", testJWTSecret)
	require.NoError(t, err)

	pt, err := enc.Decrypt(legacyCT)
	require.NoError(t, err)
	assert.Equal(t, "old-git-token", pt)
}

// TestTokenEncryptor_PrimaryKeyDependsOnStorageKey verifies the storage key is
// independent of JWT_SECRET: two encryptors sharing a JWT secret but with
// different storage keys must not be able to read each other's primary-scheme
// ciphertext (so disclosing JWT_SECRET alone does not reveal stored secrets).
func TestTokenEncryptor_PrimaryKeyDependsOnStorageKey(t *testing.T) {
	encA, err := NewTokenEncryptor(testStorageKey, testJWTSecret)
	require.NoError(t, err)
	encB, err := NewTokenEncryptor("a-totally-different-storage-secret", testJWTSecret)
	require.NoError(t, err)

	ct, err := encA.Encrypt("sensitive")
	require.NoError(t, err)

	// encB shares the JWT secret (so it has the same legacy key) but a different
	// storage key; it must NOT decrypt encA's primary-scheme ciphertext.
	_, err = encB.Decrypt(ct)
	assert.Error(t, err, "different storage key must not decrypt primary ciphertext")
}
