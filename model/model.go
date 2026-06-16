package model

import "time"

type URLItem struct {
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

type URLModel struct {
	storage URLStorage
}

type URLStorage interface {
	SaveEncryptedURLs([]URLItem) error
	LoadEncryptedURLs() ([]URLItem, error)
}

func NewURLModel(storage URLStorage) *URLModel {
	return &URLModel{storage: storage}
}

func (m *URLModel) ImportURL(raw string) error {
	item := URLItem{URL: raw, CreatedAt: time.Now()}
	items, err := m.storage.LoadEncryptedURLs()
	if err != nil {
		return err
	}
	items = append(items, item)
	return m.storage.SaveEncryptedURLs(items)
}

func (m *URLModel) ListURLs() ([]URLItem, error) {
	return m.storage.LoadEncryptedURLs()
}
