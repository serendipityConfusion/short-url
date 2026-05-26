package service

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"short-url/internal/storage"
)

func TestCreateUpdatesExpiredExistingURL(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{
		record: storage.URLRecord{
			ID:          1,
			Bucket:      0,
			ShortCode:   "1",
			OriginalURL: "https://example.com/a",
			URLHash:     storage.URLHash("https://example.com/a"),
			ExpiresAt:   sql.NullTime{Time: now.Add(-time.Hour), Valid: true},
			CreatedAt:   now.Add(-2 * time.Hour),
			UpdatedAt:   now.Add(-2 * time.Hour),
		},
	}
	shortener := NewShortener(store, nil, Options{BaseURL: "http://short.test"})

	result, err := shortener.Create(context.Background(), CreateRequest{
		URL:      "https://example.com/a",
		ExpireIn: time.Hour,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !result.Reused {
		t.Fatal("expected expired existing URL to reuse the existing short code")
	}
	if result.Code != "1" {
		t.Fatalf("code = %q, want 1", result.Code)
	}
	if !store.updatedExpiresAt.Valid {
		t.Fatal("expected expires_at to be updated")
	}
	if !store.updatedExpiresAt.Time.After(now) {
		t.Fatalf("updated expires_at = %s, want after %s", store.updatedExpiresAt.Time, now)
	}
	if result.ExpiresAt == nil || !result.ExpiresAt.Equal(store.updatedExpiresAt.Time) {
		t.Fatalf("result expires_at = %v, want %s", result.ExpiresAt, store.updatedExpiresAt.Time)
	}
}

func TestCreateCoalescesConcurrentSameURL(t *testing.T) {
	store := newBlockingCreateStore()
	shortener := NewShortener(store, nil, Options{BaseURL: "http://short.test"})

	const requests = 10
	results := make(chan CreateResult, requests)
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := shortener.Create(context.Background(), CreateRequest{
				URL:      "https://example.com/a",
				ExpireIn: time.Hour,
			})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}

	<-store.findStarted
	time.Sleep(20 * time.Millisecond)
	close(store.releaseFind)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("create: %v", err)
	}
	for result := range results {
		if result.Code != "1" {
			t.Fatalf("code = %q, want 1", result.Code)
		}
	}
	if got := store.findByHashCalls.Load(); got != 1 {
		t.Fatalf("FindByHash calls = %d, want 1", got)
	}
	if got := store.insertCalls.Load(); got != 1 {
		t.Fatalf("InsertURL calls = %d, want 1", got)
	}
}

type fakeStore struct {
	record           storage.URLRecord
	updatedExpiresAt sql.NullTime
}

func (s *fakeStore) TotalBuckets() uint32 {
	return 16
}

func (s *fakeStore) InsertURL(context.Context, string, sql.NullTime) (storage.URLRecord, error) {
	return storage.URLRecord{}, storage.ErrNotFound
}

func (s *fakeStore) FindByHash(_ context.Context, urlHash string) (storage.URLRecord, error) {
	if urlHash != s.record.URLHash {
		return storage.URLRecord{}, storage.ErrNotFound
	}
	return s.record, nil
}

func (s *fakeStore) FindByCode(context.Context, string, uint64) (storage.URLRecord, error) {
	return storage.URLRecord{}, storage.ErrNotFound
}

func (s *fakeStore) UpdateShortCode(_ context.Context, record storage.URLRecord, shortCode string) error {
	s.record = record
	s.record.ShortCode = shortCode
	return nil
}

func (s *fakeStore) UpdateExpiresAt(_ context.Context, record storage.URLRecord, expiresAt sql.NullTime) error {
	s.updatedExpiresAt = expiresAt
	s.record = record
	s.record.ExpiresAt = expiresAt
	return nil
}

type blockingCreateStore struct {
	findStarted     chan struct{}
	releaseFind     chan struct{}
	findStartedOnce sync.Once
	findByHashCalls atomic.Int64
	insertCalls     atomic.Int64
}

func newBlockingCreateStore() *blockingCreateStore {
	return &blockingCreateStore{
		findStarted: make(chan struct{}),
		releaseFind: make(chan struct{}),
	}
}

func (s *blockingCreateStore) TotalBuckets() uint32 {
	return 16
}

func (s *blockingCreateStore) InsertURL(_ context.Context, originalURL string, expiresAt sql.NullTime) (storage.URLRecord, error) {
	s.insertCalls.Add(1)
	return storage.URLRecord{
		ID:          1,
		Bucket:      0,
		OriginalURL: originalURL,
		URLHash:     storage.URLHash(originalURL),
		ExpiresAt:   expiresAt,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}, nil
}

func (s *blockingCreateStore) FindByHash(context.Context, string) (storage.URLRecord, error) {
	s.findByHashCalls.Add(1)
	s.findStartedOnce.Do(func() {
		close(s.findStarted)
	})
	<-s.releaseFind
	return storage.URLRecord{}, storage.ErrNotFound
}

func (s *blockingCreateStore) FindByCode(context.Context, string, uint64) (storage.URLRecord, error) {
	return storage.URLRecord{}, storage.ErrNotFound
}

func (s *blockingCreateStore) UpdateShortCode(context.Context, storage.URLRecord, string) error {
	return nil
}

func (s *blockingCreateStore) UpdateExpiresAt(context.Context, storage.URLRecord, sql.NullTime) error {
	return nil
}
