package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"cot-backend/internal/transformer"
)

type Router struct {
	pipeline *transformer.Pipeline
}

func NewRouter(model *transformer.Model) *mux.Router {
	r := &Router{pipeline: transformer.NewPipeline(model)}
	mx := mux.NewRouter()

	mx.HandleFunc("/health", r.health).Methods("GET")
	mx.HandleFunc("/api/reason", r.reason).Methods("POST")
	mx.HandleFunc("/api/reason/stream", r.reasonStream).Methods("POST")
	mx.HandleFunc("/api/attention/{layer}/{head}", r.attention).Methods("POST")
	mx.HandleFunc("/api/activations", r.activations).Methods("POST")

	return mx
}

// ---- Handlers ----

func (r *Router) health(w http.ResponseWriter, _ *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "1.0.0"})
}

// POST /api/reason
// Body: {"query": "..."}
// Returns: full ReasoningTrace as JSON
func (r *Router) reason(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Query == "" {
		http.Error(w, `{"error":"query required"}`, http.StatusBadRequest)
		return
	}

	trace := r.pipeline.Run(body.Query)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(trace)
}

// POST /api/reason/stream
// Streams each layer's output as Server-Sent Events so the frontend can
// animate the reasoning graph as it builds up.
// SSE events:
//   - event: cot_step     → one CoTStep JSON
//   - event: attention     → one AttentionSnapshot JSON
//   - event: activation    → one LayerActivation JSON
//   - event: tool_call     → one ToolCall JSON
//   - event: done          → final answer string
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

	emit := func(event string, payload any) {
		data, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
		time.Sleep(80 * time.Millisecond) // pacing for frontend animation
	}

	// Stream CoT steps
	for _, step := range trace.CoTSteps {
		emit("cot_step", step)
	}
	// Stream tool calls
	for _, tc := range trace.ToolCalls {
		emit("tool_call", tc)
	}
	// Stream attention snapshots (layer 0 only to avoid flooding; client can request more)
	for _, snap := range trace.Attentions {
		if snap.Layer == 0 {
			emit("attention", snap)
		}
	}
	// Stream activations
	for _, act := range trace.Activations {
		emit("activation", act)
	}
	// Done
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
