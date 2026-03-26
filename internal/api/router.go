package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"cot-backend/internal/auth"
	"cot-backend/internal/cache"
	"cot-backend/internal/kafka"
	"cot-backend/internal/transformer"
)

// Router holds application-level dependencies shared across HTTP handlers.
type Router struct {
	pipeline *transformer.Pipeline
	kafka    *kafka.Service
	cache    *cache.Service
}

// NewRouter wires all HTTP routes and returns a ready mux.Router.
//
// Route layout:
//
//	Public  (no auth):
//	  GET  /health
//	  POST /auth/login
//
//	Protected (Bearer JWT required):
//	  GET  /auth/me
//	  POST /api/reason              ← cache-enabled
//	  POST /api/reason/stream       ← cache-aware SSE stream
//	  POST /api/attention/{layer}/{head}
//	  POST /api/activations
//	  GET  /api/kafka/status
//	  GET  /api/cache/status
//	  DELETE /api/cache             ← invalidate a query's cached trace
func NewRouter(model *transformer.Model, kafkaSvc *kafka.Service, cacheSvc *cache.Service) *mux.Router {
	r := &Router{
		pipeline: transformer.NewPipeline(model),
		kafka:    kafkaSvc,
		cache:    cacheSvc,
	}
	mx := mux.NewRouter()

	// ── Public routes ────────────────────────────────────────────────────────
	mx.HandleFunc("/health", r.health).Methods("GET")

	// ── Protected subrouter — all routes require a valid Bearer JWT ──────────
	protected := mx.NewRoute().Subrouter()
	protected.Use(auth.Middleware)

	protected.HandleFunc("/auth/me", r.me).Methods("GET")
	protected.HandleFunc("/api/reason", r.reason).Methods("POST")
	protected.HandleFunc("/api/reason/stream", r.reasonStream).Methods("POST")
	protected.HandleFunc("/api/attention/{layer}/{head}", r.attention).Methods("POST")
	protected.HandleFunc("/api/activations", r.activations).Methods("POST")
	protected.HandleFunc("/api/kafka/status", r.kafkaStatus).Methods("GET")
	protected.HandleFunc("/api/cache/status", r.cacheStatus).Methods("GET")
	protected.HandleFunc("/api/cache", r.cacheInvalidate).Methods("DELETE")

	return mx
}

// ── Handlers ────────────────────────────────────────────────────────────────

func (r *Router) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "1.0.0"})
}

// POST /api/reason
// Body: {"query": "..."}
//
// Cache flow:
//  1. Hash query → Redis key
//  2. Cache HIT  → return cached JSON immediately (X-Cache: HIT)
//  3. Cache MISS → run pipeline → store in Redis → return (X-Cache: MISS)
func (r *Router) reason(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Query == "" {
		http.Error(w, `{"error":"query required"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// ── Cache lookup ──────────────────────────────────────────────────────────
	if trace, ok := r.cache.GetTrace(req.Context(), body.Query); ok {
		w.Header().Set("X-Cache", "HIT")
		json.NewEncoder(w).Encode(trace)
		return
	}
	w.Header().Set("X-Cache", "MISS")

	// ── Pipeline run ──────────────────────────────────────────────────────────
	trace := r.pipeline.Run(body.Query)

	// Store in cache (non-blocking — fire and forget).
	go r.cache.SetTrace(req.Context(), body.Query, trace)

	// Publish to Kafka (non-blocking).
	r.kafka.PublishTrace(req.Context(), trace)
	r.kafka.PublishEvents(req.Context(), trace)

	json.NewEncoder(w).Encode(trace)
}

// POST /api/reason/stream
// Streams SSE events. Checks cache first; if hit, replays events from the
// cached trace rather than re-running the pipeline.
func (r *Router) reasonStream(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Query == "" {
		http.Error(w, `{"error":"query required"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	emit := func(event string, payload any) {
		data, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
		time.Sleep(80 * time.Millisecond)
	}

	// Try cache first to avoid re-running the pipeline.
	trace, hit := r.cache.GetTrace(req.Context(), body.Query)
	if !hit {
		trace = r.pipeline.Run(body.Query)
		go r.cache.SetTrace(req.Context(), body.Query, trace)
		r.kafka.PublishTrace(req.Context(), trace)
		r.kafka.PublishEvents(req.Context(), trace)
	}

	// Emit header event so the client knows cache status.
	emit("meta", map[string]bool{"cache_hit": hit})

	for _, step := range trace.CoTSteps {
		emit("cot_step", step)
	}
	for _, tc := range trace.ToolCalls {
		emit("tool_call", tc)
	}
	for _, snap := range trace.Attentions {
		if snap.Layer == 0 {
			emit("attention", snap)
		}
	}
	for _, act := range trace.Activations {
		emit("activation", act)
	}
	emit("done", map[string]string{"answer": trace.Answer})
}

// POST /api/attention/{layer}/{head}
func (r *Router) attention(w http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	layer, head := vars["layer"], vars["head"]

	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Query == "" {
		http.Error(w, `{"error":"query required"}`, http.StatusBadRequest)
		return
	}

	// Re-use cached trace if available.
	trace, ok := r.cache.GetTrace(req.Context(), body.Query)
	if !ok {
		trace = r.pipeline.Run(body.Query)
		go r.cache.SetTrace(req.Context(), body.Query, trace)
	}

	for _, snap := range trace.Attentions {
		if fmt.Sprint(snap.Layer) == layer && fmt.Sprint(snap.Head) == head {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(snap)
			return
		}
	}
	http.Error(w, `{"error":"layer/head not found"}`, http.StatusNotFound)
}

// POST /api/activations
func (r *Router) activations(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Query == "" {
		http.Error(w, `{"error":"query required"}`, http.StatusBadRequest)
		return
	}

	trace, ok := r.cache.GetTrace(req.Context(), body.Query)
	if !ok {
		trace = r.pipeline.Run(body.Query)
		go r.cache.SetTrace(req.Context(), body.Query, trace)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"activations": trace.Activations,
		"tokens":      trace.Tokens,
	})
}

// GET /api/kafka/status
func (r *Router) kafkaStatus(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := "disabled"
	if r.kafka.Enabled() {
		status = "enabled"
	}
	json.NewEncoder(w).Encode(map[string]any{
		"kafka_enabled": r.kafka.Enabled(),
		"status":        status,
		"topics": map[string]string{
			"requests": kafka.TopicReasoningRequests,
			"traces":   kafka.TopicReasoningTraces,
			"events":   kafka.TopicCotEvents,
		},
	})
	_ = context.Background()
}

// GET /api/cache/status
func (r *Router) cacheStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"cache_enabled": r.cache.Enabled(),
		"status":        map[bool]string{true: "enabled", false: "disabled"}[r.cache.Enabled()],
		"key_prefix":    "noetic:trace:",
		"note":          "TTL controlled by REDIS_CACHE_TTL env var (seconds, default 3600)",
	})
}

// DELETE /api/cache
// Body: {"query":"..."} — removes the cached trace for a specific query.
func (r *Router) cacheInvalidate(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Query == "" {
		http.Error(w, `{"error":"query required"}`, http.StatusBadRequest)
		return
	}

	if err := r.cache.Invalidate(req.Context(), body.Query); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status":  "invalidated",
		"query":   body.Query,
		"message": "cached trace removed",
	})
}
