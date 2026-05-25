package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"strings"
	"time"
)

var ErrNotFound = errors.New("short url not found")

type URLRecord struct {
	ID          uint64
	Bucket      uint32
	ShortCode   string
	OriginalURL string
	URLHash     string
	ExpiresAt   sql.NullTime
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ShardedStore struct {
	dbs          []*sql.DB
	tableCount   int
	totalBuckets uint32
}

func NewShardedStore(dbs []*sql.DB, tableCount int) (*ShardedStore, error) {
	if len(dbs) == 0 {
		return nil, errors.New("at least one database is required")
	}
	if tableCount <= 0 {
		return nil, errors.New("table count must be positive")
	}
	return &ShardedStore{
		dbs:          dbs,
		tableCount:   tableCount,
		totalBuckets: uint32(len(dbs) * tableCount),
	}, nil
}

func (s *ShardedStore) TotalBuckets() uint32 {
	return s.totalBuckets
}

func URLHash(originalURL string) string {
	sum := sha256.Sum256([]byte(originalURL))
	return fmt.Sprintf("%x", sum[:])
}

func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("url is required")
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("url must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("only http and https urls are supported")
	}
	return parsed.String(), nil
}

func (s *ShardedStore) BucketForURLHash(urlHash string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(urlHash))
	return h.Sum32() % s.totalBuckets
}

func (s *ShardedStore) InsertURL(ctx context.Context, originalURL string, expiresAt sql.NullTime) (URLRecord, error) {
	hash := URLHash(originalURL)
	bucket := s.BucketForURLHash(hash)
	db, table := s.route(bucket)

	result, err := db.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s (url_hash, original_url, expires_at) VALUES (?, ?, ?)", tableName(table)),
		hash, originalURL, nullableTimeArg(expiresAt),
	)
	if err != nil {
		if isDuplicateEntry(err) {
			return s.FindByHash(ctx, hash)
		}
		return URLRecord{}, err
	}

	localID, err := result.LastInsertId()
	if err != nil {
		return URLRecord{}, err
	}
	if localID <= 0 {
		return URLRecord{}, errors.New("mysql returned empty last insert id")
	}

	return URLRecord{
		ID:          uint64(localID),
		Bucket:      bucket,
		OriginalURL: originalURL,
		URLHash:     hash,
		ExpiresAt:   expiresAt,
	}, nil
}

func (s *ShardedStore) FindByHash(ctx context.Context, urlHash string) (URLRecord, error) {
	bucket := s.BucketForURLHash(urlHash)
	db, table := s.route(bucket)

	var record URLRecord
	record.Bucket = bucket
	record.URLHash = urlHash
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT id, short_code, original_url, expires_at, created_at, updated_at FROM %s WHERE url_hash = ? LIMIT 1", tableName(table)),
		urlHash,
	).Scan(&record.ID, &record.ShortCode, &record.OriginalURL, &record.ExpiresAt, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return URLRecord{}, ErrNotFound
	}
	if err != nil {
		return URLRecord{}, err
	}
	return record, nil
}

func (s *ShardedStore) FindByCode(ctx context.Context, code string, globalID uint64) (URLRecord, error) {
	localID, bucket, err := SplitGlobalID(globalID, s.totalBuckets)
	if err != nil {
		return URLRecord{}, err
	}
	db, table := s.route(bucket)

	var record URLRecord
	record.ID = localID
	record.Bucket = bucket
	err = db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT short_code, original_url, url_hash, expires_at, created_at, updated_at FROM %s WHERE id = ? LIMIT 1", tableName(table)),
		localID,
	).Scan(&record.ShortCode, &record.OriginalURL, &record.URLHash, &record.ExpiresAt, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return URLRecord{}, ErrNotFound
	}
	if err != nil {
		return URLRecord{}, err
	}
	if record.ShortCode != "" && record.ShortCode != code {
		return URLRecord{}, ErrNotFound
	}
	return record, nil
}

func (s *ShardedStore) UpdateShortCode(ctx context.Context, record URLRecord, shortCode string) error {
	db, table := s.route(record.Bucket)
	_, err := db.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET short_code = ? WHERE id = ?", tableName(table)),
		shortCode, record.ID,
	)
	return err
}

func (s *ShardedStore) route(bucket uint32) (*sql.DB, int) {
	dbIndex := int(bucket) / s.tableCount
	tableIndex := int(bucket) % s.tableCount
	return s.dbs[dbIndex], tableIndex
}

func tableName(index int) string {
	return fmt.Sprintf("short_urls_%02d", index)
}

func nullableTimeArg(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time
}

func isDuplicateEntry(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate entry") || strings.Contains(msg, "error 1062")
}
