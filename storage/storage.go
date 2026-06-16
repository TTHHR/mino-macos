package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"

	"golang.org/x/crypto/scrypt"

	"minoMac_wails/model"
)

const DefaultPassphrase = "minoMac-default-passphrase"

type FileStorage struct {
	path string
	key  []byte
}

func NewFileStorage(path, passphrase string) (*FileStorage, error) {
	key, err := deriveKey(passphrase)
	if err != nil {
		return nil, err
	}
	return &FileStorage{path: path, key: key}, nil
}

func deriveKey(passphrase string) ([]byte, error) {
	const N = 1 << 15
	const r = 8
	const p = 1
	const keyLen = 32
	salt := []byte("minomac-salt-2026")
	return scrypt.Key([]byte(passphrase), salt, N, r, p, keyLen)
}

func (s *FileStorage) SaveEncryptedURLs(items []model.URLItem) error {
	payload, err := json.Marshal(items)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	ciphertext := aead.Seal(nonce, nonce, payload, nil)
	return os.WriteFile(s.path, ciphertext, 0o600)
}

func (s *FileStorage) LoadEncryptedURLs() ([]model.URLItem, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := aead.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("invalid encrypted data")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	var items []model.URLItem
	if err := json.Unmarshal(plaintext, &items); err != nil {
		return nil, err
	}
	return items, nil
}
