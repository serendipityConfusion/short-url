package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"short-url/internal/service"
	"short-url/internal/storage"
)

type Shortener interface {
	Create(rctx context.Context, req service.CreateRequest) (service.CreateResult, error)
	Resolve(rctx context.Context, code string) (string, error)
	Lookup(rctx context.Context, code string) (service.LookupResult, error)
}

type Options struct {
	InternalAPIToken   string
	InternalAuthMode   string
	InternalAuthHeader string
	BatchCreateLimit   int
}

func RegisterRoutes(mux *http.ServeMux, shortener *service.Shortener, opts Options) {
	if opts.BatchCreateLimit == 0 {
		opts.BatchCreateLimit = 100
	}
	if opts.InternalAuthHeader == "" {
		opts.InternalAuthHeader = "X-Internal-Token"
	}
	h := &handler{
		shortener:          shortener,
		internalAPIToken:   opts.InternalAPIToken,
		internalAuthMode:   normalizeAuthMode(opts.InternalAuthMode, opts.InternalAPIToken),
		internalAuthHeader: opts.InternalAuthHeader,
		batchCreateLimit:   opts.BatchCreateLimit,
	}
	mux.HandleFunc("POST /api/v1/short-links", h.create)
	mux.HandleFunc("POST /internal/api/v1/short-links/batch", h.internalBatchCreate)
	mux.HandleFunc("GET /internal/api/v1/short-links/{code}", h.internalLookup)
	mux.HandleFunc("GET /{code}", h.redirect)
}

type handler struct {
	shortener          *service.Shortener
	internalAPIToken   string
	internalAuthMode   string
	internalAuthHeader string
	batchCreateLimit   int
}

type createRequest struct {
	URL      string `json:"url"`
	ExpireAt string `json:"expire_at"`
	ExpireIn string `json:"expire_in"`
}

type batchCreateRequest struct {
	Items []createRequest `json:"items"`
}

type batchCreateResponse struct {
	Results []batchCreateItemResult `json:"results"`
}

type batchCreateItemResult struct {
	Index  int                   `json:"index"`
	OK     bool                  `json:"ok"`
	Result *service.CreateResult `json:"result,omitempty"`
	Error  string                `json:"error,omitempty"`
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "empty request body")
		return
	}

	var payload createRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	req, err := payload.toServiceRequest()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := h.shortener.Create(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *handler) internalBatchCreate(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternalAuth(w, r) {
		return
	}
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "empty request body")
		return
	}

	var payload batchCreateRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if len(payload.Items) == 0 {
		writeError(w, http.StatusBadRequest, "items is required")
		return
	}
	if len(payload.Items) > h.batchCreateLimit {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("items exceeds limit %d", h.batchCreateLimit))
		return
	}

	results := make([]batchCreateItemResult, 0, len(payload.Items))
	for i, item := range payload.Items {
		req, err := item.toServiceRequest()
		if err != nil {
			results = append(results, batchCreateItemResult{
				Index: i,
				OK:    false,
				Error: err.Error(),
			})
			continue
		}

		result, err := h.shortener.Create(r.Context(), req)
		if err != nil {
			results = append(results, batchCreateItemResult{
				Index: i,
				OK:    false,
				Error: err.Error(),
			})
			continue
		}
		results = append(results, batchCreateItemResult{
			Index:  i,
			OK:     true,
			Result: &result,
		})
	}

	writeJSON(w, http.StatusOK, batchCreateResponse{Results: results})
}

func (h *handler) internalLookup(w http.ResponseWriter, r *http.Request) {
	if !h.requireInternalAuth(w, r) {
		return
	}

	result, err := h.shortener.Lookup(r.Context(), r.PathValue("code"))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("internal lookup %q: %v", r.PathValue("code"), err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) redirect(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimPrefix(r.URL.Path, "/")
	if code == "" || strings.HasPrefix(code, "api/") || strings.HasPrefix(code, "internal/") || code == "healthz" {
		http.NotFound(w, r)
		return
	}

	originalURL, err := h.shortener.Resolve(r.Context(), code)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("resolve %q: %v", code, err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	http.Redirect(w, r, originalURL, http.StatusFound)
}

func (h *handler) requireInternalAuth(w http.ResponseWriter, r *http.Request) bool {
	switch h.internalAuthMode {
	case "disabled":
		http.NotFound(w, r)
		return false
	case "none":
		return true
	case "bearer":
		if h.matchBearerToken(r) {
			return true
		}
	case "header":
		if h.matchHeaderToken(r) {
			return true
		}
	case "any":
		if h.matchBearerToken(r) || h.matchHeaderToken(r) {
			return true
		}
	}

	w.Header().Set("WWW-Authenticate", `Bearer realm="short-url-internal"`)
	writeError(w, http.StatusUnauthorized, "unauthorized")
	return false
}

func (h *handler) matchBearerToken(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return secureCompare(strings.TrimSpace(auth[len("Bearer "):]), h.internalAPIToken)
	}
	return false
}

func (h *handler) matchHeaderToken(r *http.Request) bool {
	return secureCompare(strings.TrimSpace(r.Header.Get(h.internalAuthHeader)), h.internalAPIToken)
}

func normalizeAuthMode(mode string, token string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "" {
		return mode
	}
	if token == "" {
		return "disabled"
	}
	return "any"
}

func secureCompare(got, want string) bool {
	if got == "" || want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (r createRequest) toServiceRequest() (service.CreateRequest, error) {
	req := service.CreateRequest{
		URL: r.URL,
	}
	if r.ExpireAt != "" {
		expireAt, err := time.Parse(time.RFC3339, r.ExpireAt)
		if err != nil {
			return service.CreateRequest{}, errors.New("expire_at must be RFC3339")
		}
		req.ExpireAt = &expireAt
	}
	if r.ExpireIn != "" {
		expireIn, err := time.ParseDuration(r.ExpireIn)
		if err != nil {
			return service.CreateRequest{}, errors.New("expire_in must be a Go duration, for example 24h")
		}
		req.ExpireIn = expireIn
	}
	return req, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
