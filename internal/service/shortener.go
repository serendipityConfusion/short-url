package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"short-url/internal/base62"
	"short-url/internal/storage"
)

type Store interface {
	TotalBuckets() uint32
	InsertURL(ctx context.Context, originalURL string, expiresAt sql.NullTime) (storage.URLRecord, error)
	FindByHash(ctx context.Context, urlHash string) (storage.URLRecord, error)
	FindByCode(ctx context.Context, code string, globalID uint64) (storage.URLRecord, error)
	UpdateShortCode(ctx context.Context, record storage.URLRecord, shortCode string) error
}

type Options struct {
	BaseURL       string
	CodeTTL       time.Duration
	LongURLTTL    time.Duration
	LockTTL       time.Duration
	DefaultExpire time.Duration
}

type CreateRequest struct {
	URL       string
	ExpireAt  *time.Time
	ExpireIn  time.Duration
	RequestID string
}

type CreateResult struct {
	Code      string     `json:"code"`
	ShortURL  string     `json:"short_url"`
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Reused    bool       `json:"reused"`
}

type LookupResult struct {
	ID        uint64     `json:"id"`
	Bucket    uint32     `json:"bucket"`
	Code      string     `json:"code"`
	ShortURL  string     `json:"short_url"`
	URL       string     `json:"url"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Expired   bool       `json:"expired"`
}

type Shortener struct {
	store Store
	redis *redis.Client
	opts  Options
}

func NewShortener(store Store, redisClient *redis.Client, opts Options) *Shortener {
	if opts.CodeTTL == 0 {
		opts.CodeTTL = 24 * time.Hour
	}
	if opts.LongURLTTL == 0 {
		opts.LongURLTTL = 24 * time.Hour
	}
	if opts.LockTTL == 0 {
		opts.LockTTL = 3 * time.Second
	}
	return &Shortener{store: store, redis: redisClient, opts: opts}
}

func (s *Shortener) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	normalized, err := storage.NormalizeURL(req.URL)
	if err != nil {
		return CreateResult{}, err
	}

	urlHash := storage.URLHash(normalized)
	longKey := "short-url:long:" + urlHash
	if code, ok := s.getString(ctx, longKey); ok {
		return CreateResult{
			Code:     code,
			ShortURL: s.shortURL(code),
			URL:      normalized,
			Reused:   true,
		}, nil
	}

	unlock := s.tryLock(ctx, "short-url:lock:"+urlHash)
	if unlock != nil {
		defer unlock()
	}

	if code, ok := s.getString(ctx, longKey); ok {
		return CreateResult{
			Code:     code,
			ShortURL: s.shortURL(code),
			URL:      normalized,
			Reused:   true,
		}, nil
	}
	if record, err := s.store.FindByHash(ctx, urlHash); err == nil {
		code, err := s.ensureCode(ctx, record)
		if err != nil {
			return CreateResult{}, err
		}
		s.cacheCreateResult(ctx, code, normalized, urlHash, record.ExpiresAt)
		return resultFromRecord(s.shortURL(code), code, record, true), nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return CreateResult{}, err
	}

	expiresAt := s.expiration(req)
	record, err := s.store.InsertURL(ctx, normalized, expiresAt)
	if err != nil {
		return CreateResult{}, err
	}
	code, err := s.ensureCode(ctx, record)
	if err != nil {
		return CreateResult{}, err
	}

	s.cacheCreateResult(ctx, code, normalized, urlHash, record.ExpiresAt)
	return resultFromRecord(s.shortURL(code), code, record, false), nil
}

func (s *Shortener) Resolve(ctx context.Context, code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", storage.ErrNotFound
	}
	if originalURL, ok := s.getString(ctx, "short-url:code:"+code); ok {
		return originalURL, nil
	}

	globalID, err := base62.Decode(code)
	if err != nil {
		return "", storage.ErrNotFound
	}
	record, err := s.store.FindByCode(ctx, code, globalID)
	if err != nil {
		return "", err
	}
	if record.ExpiresAt.Valid && time.Now().After(record.ExpiresAt.Time) {
		return "", storage.ErrNotFound
	}

	s.cacheCode(ctx, code, record.OriginalURL, record.ExpiresAt)
	return record.OriginalURL, nil
}

func (s *Shortener) Lookup(ctx context.Context, code string) (LookupResult, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return LookupResult{}, storage.ErrNotFound
	}

	globalID, err := base62.Decode(code)
	if err != nil {
		return LookupResult{}, storage.ErrNotFound
	}
	record, err := s.store.FindByCode(ctx, code, globalID)
	if err != nil {
		return LookupResult{}, err
	}

	expired := record.ExpiresAt.Valid && time.Now().After(record.ExpiresAt.Time)
	if !expired {
		s.cacheCode(ctx, code, record.OriginalURL, record.ExpiresAt)
	}
	return lookupResultFromRecord(s.shortURL(code), code, record, expired), nil
}

func (s *Shortener) ensureCode(ctx context.Context, record storage.URLRecord) (string, error) {
	if record.ShortCode != "" {
		return record.ShortCode, nil
	}
	globalID, err := storage.ComposeGlobalID(record.ID, record.Bucket, s.store.TotalBuckets())
	if err != nil {
		return "", err
	}
	code := base62.Encode(globalID)
	if err := s.store.UpdateShortCode(ctx, record, code); err != nil {
		return "", err
	}
	return code, nil
}

func (s *Shortener) expiration(req CreateRequest) sql.NullTime {
	if req.ExpireAt != nil {
		return sql.NullTime{Time: req.ExpireAt.UTC(), Valid: true}
	}
	if req.ExpireIn > 0 {
		return sql.NullTime{Time: time.Now().Add(req.ExpireIn).UTC(), Valid: true}
	}
	if s.opts.DefaultExpire > 0 {
		return sql.NullTime{Time: time.Now().Add(s.opts.DefaultExpire).UTC(), Valid: true}
	}
	return sql.NullTime{}
}

func (s *Shortener) shortURL(code string) string {
	if s.opts.BaseURL == "" {
		return code
	}
	return fmt.Sprintf("%s/%s", strings.TrimRight(s.opts.BaseURL, "/"), code)
}

func (s *Shortener) cacheCreateResult(ctx context.Context, code, originalURL, urlHash string, expiresAt sql.NullTime) {
	if ttl, ok := ttlBeforeExpiration(s.opts.LongURLTTL, expiresAt); ok {
		s.setString(ctx, "short-url:long:"+urlHash, code, ttl)
	}
	s.cacheCode(ctx, code, originalURL, expiresAt)
}

func (s *Shortener) cacheCode(ctx context.Context, code, originalURL string, expiresAt sql.NullTime) {
	if ttl, ok := ttlBeforeExpiration(s.opts.CodeTTL, expiresAt); ok {
		s.setString(ctx, "short-url:code:"+code, originalURL, ttl)
	}
}

func ttlBeforeExpiration(ttl time.Duration, expiresAt sql.NullTime) (time.Duration, bool) {
	if !expiresAt.Valid {
		return ttl, true
	}
	remaining := time.Until(expiresAt.Time)
	if remaining <= 0 {
		return 0, false
	}
	if ttl <= 0 || remaining < ttl {
		return remaining, true
	}
	return ttl, true
}

func (s *Shortener) getString(ctx context.Context, key string) (string, bool) {
	if s.redis == nil {
		return "", false
	}
	value, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		return "", false
	}
	return value, true
}

func (s *Shortener) setString(ctx context.Context, key, value string, ttl time.Duration) {
	if s.redis == nil {
		return
	}
	_ = s.redis.Set(ctx, key, value, ttl).Err()
}

func (s *Shortener) tryLock(ctx context.Context, key string) func() {
	if s.redis == nil {
		return nil
	}
	token := fmt.Sprintf("%d", time.Now().UnixNano())
	ok, err := s.redis.SetNX(ctx, key, token, s.opts.LockTTL).Result()
	if err != nil || !ok {
		return nil
	}
	return func() {
		script := redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
end
return 0
`)
		_ = script.Run(context.Background(), s.redis, []string{key}, token).Err()
	}
}

func resultFromRecord(shortURL, code string, record storage.URLRecord, reused bool) CreateResult {
	var expiresAt *time.Time
	if record.ExpiresAt.Valid {
		expiresAt = &record.ExpiresAt.Time
	}
	return CreateResult{
		Code:      code,
		ShortURL:  shortURL,
		URL:       record.OriginalURL,
		ExpiresAt: expiresAt,
		Reused:    reused,
	}
}

func lookupResultFromRecord(shortURL, code string, record storage.URLRecord, expired bool) LookupResult {
	var expiresAt *time.Time
	if record.ExpiresAt.Valid {
		expiresAt = &record.ExpiresAt.Time
	}
	return LookupResult{
		ID:        record.ID,
		Bucket:    record.Bucket,
		Code:      code,
		ShortURL:  shortURL,
		URL:       record.OriginalURL,
		ExpiresAt: expiresAt,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
		Expired:   expired,
	}
}
