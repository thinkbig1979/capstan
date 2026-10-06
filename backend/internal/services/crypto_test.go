package services

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"strings"
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

	ct, err := enc.Encrypt("hunter2", "settings:test")
	require.NoError(t, err)
	assert.NotEqual(t, "hunter2", ct)

	pt, err := enc.Decrypt(ct, "settings:test")
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

	pt, err := enc.Decrypt(legacyCT, "settings:git_https_token")
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

	ct, err := encA.Encrypt("sensitive", "settings:test")
	require.NoError(t, err)

	// encB shares the JWT secret (so it has the same legacy key) but a different
	// storage key; it must NOT decrypt encA's primary-scheme ciphertext.
	_, err = encB.Decrypt(ct, "settings:test")
	assert.Error(t, err, "different storage key must not decrypt primary ciphertext")
}

// TestTokenEncryptor_V2BindsAssociatedData is the unit half of agent-os-n4ca.7
// (the database half is in secrets_at_rest_test.go): a v2 value opens only
// with the associated data it was sealed with.
func TestTokenEncryptor_V2BindsAssociatedData(t *testing.T) {
	enc, err := NewTokenEncryptor(testStorageKey, testJWTSecret)
	require.NoError(t, err)

	ct, err := enc.Encrypt("hunter2", "settings:restic_password")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ct, sealedV2Prefix), "new ciphertext must be v2, got %q", ct)

	pt, err := enc.Decrypt(ct, "settings:restic_password")
	require.NoError(t, err)
	assert.Equal(t, "hunter2", pt)

	_, err = enc.Decrypt(ct, "settings:git_https_token")
	assert.Error(t, err, "a v2 value must not open under another location's associated data")

	_, err = enc.Decrypt(strings.TrimPrefix(ct, sealedV2Prefix), "settings:restic_password")
	assert.Error(t, err, "a prefix-stripped v2 value must not open through the v1 arm")
}

// TestTokenEncryptor_RefusesEmptyAssociatedData: an empty aad would make every
// value sealed with it interchangeable, so neither side accepts one.
func TestTokenEncryptor_RefusesEmptyAssociatedData(t *testing.T) {
	enc, err := NewTokenEncryptor(testStorageKey, testJWTSecret)
	require.NoError(t, err)

	_, err = enc.Encrypt("hunter2", "")
	assert.ErrorIs(t, err, errMissingAAD)

	ct, err := enc.Encrypt("hunter2", "settings:x")
	require.NoError(t, err)
	_, err = enc.Decrypt(ct, "")
	assert.ErrorIs(t, err, errMissingAAD)
}

// TestTokenEncryptor_DecryptsCommittedPreV2Fixtures: ciphertexts sealed by the
// pre-n4ca.7 crypto.go (testdata/crypto_v1_fixtures.json) still decrypt, under
// any associated data since they carry none, and a v1 value is never mistaken
// for v2.
func TestTokenEncryptor_DecryptsCommittedPreV2Fixtures(t *testing.T) {
	f := loadCryptoFixtures(t)
	enc, err := NewTokenEncryptor(f.StorageSecret, f.JWTSecret)
	require.NoError(t, err)

	for name, set := range map[string]map[string]struct{ Plaintext, Ciphertext string }{
		"v1_primary": f.V1Primary,
		"legacy":     f.Legacy,
	} {
		for key, e := range set {
			require.False(t, strings.HasPrefix(e.Ciphertext, sealedV2Prefix))
			pt, err := enc.Decrypt(e.Ciphertext, "settings:"+key)
			require.NoError(t, err, "%s/%s", name, key)
			assert.Equal(t, e.Plaintext, pt, "%s/%s", name, key)
		}
	}

	// The legacy fixtures need the legacy key: without JWT_SECRET they fail,
	// which shows the arm above was the legacy one and not the primary.
	noLegacy, err := NewTokenEncryptor(f.StorageSecret, "")
	require.NoError(t, err)
	_, err = noLegacy.Decrypt(f.Legacy["restic_password"].Ciphertext, "settings:restic_password")
	assert.Error(t, err)
}

// TestTokenEncryptor_PrefixedV1DoesNotDecrypt: the opposite forgery to a
// stripped prefix. A v1 ciphertext (no associated data) with the v2 prefix
// added must fail: a prefixed value has exactly one valid reading. If the v2
// arm fell back to the unbound arms, an old v1 restic_password ciphertext from
// a backup, prefixed and planted in restic_repository (whose unprefixed values
// are never decrypted), would be decrypted and served as the repository.
func TestTokenEncryptor_PrefixedV1DoesNotDecrypt(t *testing.T) {
	f := loadCryptoFixtures(t)
	enc, err := NewTokenEncryptor(f.StorageSecret, f.JWTSecret)
	require.NoError(t, err)

	for name, set := range map[string]map[string]struct{ Plaintext, Ciphertext string }{
		"v1_primary": f.V1Primary,
		"legacy":     f.Legacy,
	} {
		forged := sealedV2Prefix + set["restic_password"].Ciphertext
		pt, err := enc.Decrypt(forged, "settings:restic_repository")
		assert.Error(t, err, "%s: a prefixed pre-v2 ciphertext decrypted to %q", name, pt)
	}
}
