# CoT Visualization — Go Backend

A production-grade Chain-of-Thought reasoning backend built on a **custom transformer from scratch** in pure Go.
Captures all four reasoning signal types in a single forward pass.

---

## Architecture

```
main.go
├── internal/
│   ├── transformer/
│   │   ├── types.go       — shared data structures (ReasoningTrace, CoTStep, ToolCall, …)
│   │   ├── math.go        — matrix ops, softmax, layer norm, positional encoding
│   │   ├── attention.go   — multi-head self-attention with per-head snapshot extraction
│   │   ├── block.go       — transformer block (attention + FF + residuals + LN)
│   │   ├── model.go       — full model: embedding, stacked blocks, decoder
│   │   └── pipeline.go    — CoT prompt builder, autoregressive generation, step parser
│   └── api/
│       └── router.go      — HTTP handlers + SSE streaming endpoint
└── Dockerfile
```

### Reasoning Capture Modes

| Mode | Where captured | JSON field |
|---|---|---|
| **Prompt-guided CoT** | `pipeline.go` — system prompt + step parser | `cot_steps[]` |
| **Attention extraction** | `attention.go` — per-head softmax snapshot | `attentions[]` |
| **Layer activations** | `block.go` — mean |activation| post-FF | `activations[]` |
| **Tool-call traces** | `pipeline.go` — `<tool>` tag interception | `tool_calls[]` |

---

## Quick Start

```bash
# Install dependencies
go mod tidy

# Run tests (including benchmarks)
go test ./... -v
go test ./internal/transformer/... -bench=. -benchmem

# Start server
go run main.go

# With custom port
PORT=9000 go run main.go
```

---

## API Reference

### `GET /health`
```json
{"status": "ok", "version": "1.0.0"}
```

---

### `POST /api/reason`
Full synchronous reasoning trace.

**Request**
```json
{"query": "explain how attention works in transformers"}
```

**Response** — `ReasoningTrace`
```json
{
  "query": "explain how attention...",
  "answer": "Therefore the answer follows...",
  "tokens": ["explain", "how", "attention", "works"],
  "cot_steps": [
    {"index": 0, "step_type": "premise", "text": "...", "confidence": 0.87},
    {"index": 1, "step_type": "inference", "text": "...", "confidence": 0.74},
    {"index": 2, "step_type": "tool_call", "text": "...", "confidence": 0.61},
    {"index": 5, "step_type": "conclusion", "text": "...", "confidence": 0.92}
  ],
  "attentions": [
    {"layer": 0, "head": 0, "weights": [[0.9, 0.05, ...], ...]},
    ...
  ],
  "activations": [
    {"layer": 0, "token_means": [0.42, 0.38, ...]},
    ...
  ],
  "tool_calls": [
    {"name": "calculator", "inputs": {"query": "..."}, "output": "[simulated result]"}
  ]
}
```

---

### `POST /api/reason/stream`
Server-Sent Events stream — one event per reasoning artifact for live graph animation.

**Request**: same as `/api/reason`

**SSE event types**
```
event: cot_step     data: {"index":0,"step_type":"premise","text":"...","confidence":0.87}
event: tool_call    data: {"name":"calculator","inputs":{...},"output":"..."}
event: attention    data: {"layer":0,"head":0,"weights":[[...]]}
event: activation   data: {"layer":0,"token_means":[...]}
event: done         data: {"answer":"..."}
```

**Frontend usage (JS)**
```js
const es = new EventSource('/api/reason/stream'); // use fetch+POST in practice
es.addEventListener('cot_step', e => addNode(JSON.parse(e.data)));
es.addEventListener('attention', e => updateHeatmap(JSON.parse(e.data)));
es.addEventListener('done', e => finalizeGraph(JSON.parse(e.data)));
```

---

### `POST /api/attention/{layer}/{head}`
Retrieve attention matrix for a specific layer and head.

```bash
curl -X POST http://localhost:8080/api/attention/0/3 \
  -H 'Content-Type: application/json' \
  -d '{"query":"your query here"}'
```

---

### `POST /api/activations`
All layer activations for a query.

```bash
curl -X POST http://localhost:8080/api/activations \
  -H 'Content-Type: application/json' \
  -d '{"query":"your query here"}'
```

---

## Docker

```bash
docker build -t cot-backend .
docker run -p 8080:8080 cot-backend
```

---

## Extending

### Swap in a real tokenizer (BPE/SentencePiece)
Replace `Model.Tokenize()` in `model.go` with your tokenizer. The rest of the pipeline is tokenizer-agnostic.

### Load pre-trained weights
Add a `LoadWeights(path string)` method to `Model` that reads a binary checkpoint and populates the weight matrices. Weight shapes are documented in each struct.

### Add more tool types
Extend `extractToolCall()` in `pipeline.go` and register handlers in a `ToolRegistry` map.

### Scale attention streaming
The stream endpoint currently emits layer-0 attention only. Pass `?layers=all` and filter in `router.go` to stream all layers.

---

## Config

| Field | Default | Description |
|---|---|---|
| `VocabSize` | 32000 | Vocabulary size |
| `MaxSeqLen` | 512 | Maximum sequence length |
| `EmbedDim` | 256 | Embedding / hidden dimension |
| `NumHeads` | 8 | Attention heads per layer |
| `NumLayers` | 6 | Number of transformer blocks |
| `FFDim` | 1024 | Feed-forward hidden dimension |
