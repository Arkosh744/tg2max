package tgclient

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMemStore returns in-memory save/load functions backed by a map.
func newMemStore() (
	func(ctx context.Context, userID int64, data []byte) error,
	func(ctx context.Context, userID int64) ([]byte, error),
) {
	mu := sync.Mutex{}
	store := map[int64][]byte{}

	save := func(_ context.Context, userID int64, data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]byte, len(data))
		copy(cp, data)
		store[userID] = cp
		return nil
	}

	load := func(_ context.Context, userID int64) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		return store[userID], nil
	}

	return save, load
}

func Test_EncryptedSessionStorage_RoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}

	save, load := newMemStore()
	storage, err := NewEncryptedSessionStorage(42, key, save, load)
	require.NoError(t, err)

	original := []byte("hello, encrypted world!")
	ctx := context.Background()

	require.NoError(t, storage.StoreSession(ctx, original))

	got, err := storage.LoadSession(ctx)
	require.NoError(t, err)
	assert.Equal(t, original, got)
}

func Test_EncryptedSessionStorage_DifferentCiphertexts(t *testing.T) {
	key := make([]byte, 32)
	save1, load1 := newMemStore()
	save2, load2 := newMemStore()

	s1, err := NewEncryptedSessionStorage(1, key, save1, load1)
	require.NoError(t, err)
	s2, err := NewEncryptedSessionStorage(2, key, save2, load2)
	require.NoError(t, err)

	ctx := context.Background()
	plaintext := []byte("same plaintext")

	require.NoError(t, s1.StoreSession(ctx, plaintext))
	require.NoError(t, s2.StoreSession(ctx, plaintext))

	ct1, err := load1(ctx, 1)
	require.NoError(t, err)
	ct2, err := load2(ctx, 2)
	require.NoError(t, err)

	// AES-GCM uses a random nonce, so ciphertexts must differ.
	assert.NotEqual(t, ct1, ct2)
}

func Test_EncryptedSessionStorage_WrongKeyFails(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = 0xAB
	}

	save, load := newMemStore()
	storage, err := NewEncryptedSessionStorage(1, key, save, load)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, storage.StoreSession(ctx, []byte("secret data")))

	// Create a second storage with a different key but same userID / store.
	wrongKey := make([]byte, 32)
	for i := range wrongKey {
		wrongKey[i] = 0xCD
	}
	storage2, err := NewEncryptedSessionStorage(1, wrongKey, save, load)
	require.NoError(t, err)

	_, err = storage2.LoadSession(ctx)
	assert.Error(t, err, "decryption with wrong key must return an error")
}

func Test_EncryptedSessionStorage_ShortCiphertext(t *testing.T) {
	key := make([]byte, 32)
	ctx := context.Background()

	// Store garbage that is shorter than the GCM nonce size (12 bytes).
	short := []byte("tooshort")
	save := func(_ context.Context, userID int64, data []byte) error { return nil }
	load := func(_ context.Context, userID int64) ([]byte, error) { return short, nil }

	storage, err := NewEncryptedSessionStorage(1, key, save, load)
	require.NoError(t, err)

	_, err = storage.LoadSession(ctx)
	assert.Error(t, err, "short ciphertext must return an error")
}

func Test_EncryptedSessionStorage_KeyLengthValidation(t *testing.T) {
	save, load := newMemStore()

	// Key too short.
	_, err := NewEncryptedSessionStorage(1, make([]byte, 16), save, load)
	assert.Error(t, err)

	// Key too long.
	_, err = NewEncryptedSessionStorage(1, make([]byte, 64), save, load)
	assert.Error(t, err)

	// Exactly 32 bytes — must succeed.
	_, err = NewEncryptedSessionStorage(1, make([]byte, 32), save, load)
	assert.NoError(t, err)
}
