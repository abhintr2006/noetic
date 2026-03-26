package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"cot-backend/internal/auth"
	"cot-backend/internal/kafka"
	"cot-backend/internal/transformer"
)

// Router holds application-level dependencies shared across HTTP handlers.
type Router struct {
	pipeline *transformer.Pipeline
	kafka    *kafka.Service
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
//	  POST /api/reason
//	  POST /api/reason/stream
//	  POST /api/attention/{layer}/{head}
//	  POST /api/activations
//	  GET  /api/kafka/status
func NewRouter(model *transformer.Model, kafkaSvc *kafka.Service) *mux.Router {
	r := &Router{
		pipeline: transformer.NewPipeline(model),
		kafka:    kafkaSvc,
	}
	mx := mux.NewRouter()

	// ── Public routes ────────────────────────────────────────────────────────
	mx.HandleFunc("/health", r.health).Methods("GET")
	mx.HandleFunc("/auth/login", r.login).Methods("POST")

	// ── Protected subrouter — all routes require a valid Bearer JWT ──────────
	protected := mx.NewRoute().Subrouter()
	protected.Use(auth.Middleware)

	protected.HandleFunc("/auth/me", r.me).Methods("GET")
	protected.HandleFunc("/api/reason", r.reason).Methods("POST")
	protected.HandleFunc("/api/reason/stream", r.reasonStream).Methods("POST")
	protected.HandleFunc("/api/attention/{layer}/{head}", r.attention).Methods("POST")
	protected.HandleFunc("/api/activations", r.activations).Methods("POST")
	protected.HandleFunc("/api/kafka/status", r.kafkaStatus).Methods("GET")

	return mx
}

// ── Handlers ────────────────────────────────────────────────────────────────

func (r *Router) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "1.0.0"})
}

// POST /api/reason
// Body: {"query": "..."}
// Returns: full ReasoningTrace as JSON, and publishes it to Kafka.
func (r *Router) reason(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Query == "" {
		http.Error(w, `{"error":"query required"}`, http.StatusBadRequest)
		return
	}

	trace := r.pipeline.Run(body.Query)

	// Publish to Kafka — fire and forget, does not block response.
	r.kafka.PublishTrace(req.Context(), trace)
	r.kafka.PublishEvents(req.Context(), trace)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(trace)
}

// POST /api/reason/stream
// Streams each layer's output as Server-Sent Events so the frontend can
// animate the reasoning graph as it builds up.
// SSE events:
//   - event: cot_step     → one CoTStep JSON
//   - event: attention    → one AttentionSnapshot JSON
//   - event: activation   → one LayerActivation JSON
//   - event: tool_call    → one ToolCall JSON
//   - event: done         → final answer string
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

	trace := r.pipeline.Run(body.Query)

	// Publish full trace and events to Kafka — non-blocking.
	r.kafka.PublishTrace(req.Context(), trace)
	r.kafka.PublishEvents(req.Context(), trace)

	emit := func(event string, payload any) {
		data, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
		time.Sleep(80 * time.Millisecond) // pacing for frontend animation
	}

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
// Body: {"query":"..."} — returns the attention matrix for a specific layer+head
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

	trace := r.pipeline.Run(body.Query)

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
// Body: {"query":"..."} — returns all layer activations
func (r *Router) activations(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Query == "" {
		http.Error(w, `{"error":"query required"}`, http.StatusBadRequest)
		return
	}

	trace := r.pipeline.Run(body.Query)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"activations": trace.Activations,
		"tokens":      trace.Tokens,
	})
}

// GET /api/kafka/status
// Returns whether Kafka integration is active and which topics are configured.
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
