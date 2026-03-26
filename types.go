package transformer

// Config holds all hyperparameters for the transformer model.
type Config struct {
	VocabSize   int
	MaxSeqLen   int
	EmbedDim    int
	NumHeads    int
	NumLayers   int
	FFDim       int
	DropoutRate float64
}

func DefaultConfig() Config {
	return Config{
		VocabSize:   32000,
		MaxSeqLen:   512,
		EmbedDim:    256,
		NumHeads:    8,
		NumLayers:   6,
		FFDim:       1024,
		DropoutRate: 0.1,
	}
}

// ---- Shared data structures passed between layers and the API ----

// AttentionSnapshot captures one head's attention matrix at one layer.
type AttentionSnapshot struct {
	Layer   int         `json:"layer"`
	Head    int         `json:"head"`
	Weights [][]float64 `json:"weights"` // [SeqLen x SeqLen]
}

// LayerActivation captures the mean activation magnitude per token for a layer.
type LayerActivation struct {
	Layer      int       `json:"layer"`
	TokenMeans []float64 `json:"token_means"` // [SeqLen]
}

// CoTStep represents one structured reasoning step extracted from the model.
type CoTStep struct {
	Index    int     `json:"index"`
	StepType string  `json:"step_type"` // "premise" | "inference" | "conclusion" | "tool_call"
	Text     string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// ToolCall represents an intermediate tool invocation captured during reasoning.
type ToolCall struct {
	Name   string            `json:"name"`
	Inputs map[string]string `json:"inputs"`
	Output string            `json:"output"`
}

// ReasoningTrace is the full pipeline output for one query.
type ReasoningTrace struct {
	Query       string              `json:"query"`
	Answer      string              `json:"answer"`
	Tokens      []string            `json:"tokens"`
	CoTSteps    []CoTStep           `json:"cot_steps"`
	Attentions  []AttentionSnapshot `json:"attentions"`
	Activations []LayerActivation   `json:"activations"`
	ToolCalls   []ToolCall          `json:"tool_calls"`
}
