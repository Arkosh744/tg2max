package tgclient

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// EncryptedSessionStorage wraps raw session bytes with AES-256-GCM encryption.
// It delegates actual persistence to SaveFunc/LoadFunc callbacks (typically backed by SQLite).
type EncryptedSessionStorage struct {
	key      []byte // 32 bytes for AES-256
	userID   int64
	saveFunc func(ctx context.Context, userID int64, data []byte) error
	loadFunc func(ctx context.Context, userID int64) ([]byte, error)
}

// NewEncryptedSessionStorage creates storage that encrypts session data before saving.
// key must be exactly 32 bytes (AES-256).
func NewEncryptedSessionStorage(
	userID int64,
	key []byte,
	saveFunc func(ctx context.Context, userID int64, data []byte) error,
	loadFunc func(ctx context.Context, userID int64) ([]byte, error),
) (*EncryptedSessionStorage, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("session key must be 32 bytes, got %d", len(key))
	}
	return &EncryptedSessionStorage{
		key:      key,
		userID:   userID,
		saveFunc: saveFunc,
		loadFunc: loadFunc,
	}, nil
}

// StoreSession encrypts and persists the session data.
func (s *EncryptedSessionStorage) StoreSession(_ context.Context, data []byte) error {
	encrypted, err := s.encrypt(data)
	if err != nil {
		return fmt.Errorf("encrypt session: %w", err)
	}
	return s.saveFunc(context.Background(), s.userID, encrypted)
}

// LoadSession loads and decrypts the session data.
func (s *EncryptedSessionStorage) LoadSession(_ context.Context) ([]byte, error) {
	encrypted, err := s.loadFunc(context.Background(), s.userID)
	if err != nil {
		return nil, err
	}
	if len(encrypted) == 0 {
		return nil, nil
	}
	return s.decrypt(encrypted)
}

func (s *EncryptedSessionStorage) encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *EncryptedSessionStorage) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
