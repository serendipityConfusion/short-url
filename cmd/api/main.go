package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"short-url/internal/config"
	"short-url/internal/handler"
	"short-url/internal/service"
	"short-url/internal/storage"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to TOML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	dbs := make([]*sql.DB, 0, len(cfg.MySQL.DSNs))
	for _, dsn := range cfg.MySQL.DSNs {
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			log.Fatalf("open mysql: %v", err)
		}
		db.SetMaxOpenConns(cfg.MySQL.MaxOpenConns)
		db.SetMaxIdleConns(cfg.MySQL.MaxIdleConns)
		db.SetConnMaxLifetime(30 * time.Minute)
		dbs = append(dbs, db)
	}
	defer func() {
		for _, db := range dbs {
			_ = db.Close()
		}
	}()

	var redisClient *redis.Client
	if cfg.Redis.Addr != "" {
		redisClient = redis.NewClient(&redis.Options{
			Addr:     cfg.Redis.Addr,
			Username: cfg.Redis.Username,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		defer redisClient.Close()
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store, err := storage.NewShardedStore(dbs, cfg.MySQL.TableCount)
	if err != nil {
		log.Fatalf("create store: %v", err)
	}
	shortener := service.NewShortener(store, redisClient, service.Options{
		BaseURL:       cfg.Server.BaseURL,
		CodeTTL:       cfg.Redis.CodeTTL,
		LongURLTTL:    cfg.Redis.LongURLTTL,
		LockTTL:       cfg.Redis.LockTTL,
		DefaultExpire: cfg.ShortURL.DefaultExpire,
	})

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, shortener, handler.Options{
		InternalAPIToken:   cfg.Internal.APIToken,
		InternalAuthMode:   cfg.Internal.AuthMode,
		InternalAuthHeader: cfg.Internal.AuthHeader,
		BatchCreateLimit:   cfg.Internal.BatchCreateLimit,
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	server := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("short-url api listening on %s", cfg.Server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write json: %v", err)
	}
}
