package bedrock

import "oss.nandlabs.io/golly/genai"

// Compile-time assertion that BedrockProvider advertises structured model
// metadata to the golly model router (golly >= v1.8.0). This is additive: the
// existing Provider.Models() []string method is left untouched.
var _ genai.CapabilityProvider = (*BedrockProvider)(nil)

// bedrockCatalog is the authoritative capability metadata for the Bedrock model
// surface this provider serves. Every entry sets Provider to ProviderName so the
// router's registry keys (provider/model) line up with this provider instance.
//
// Capabilities and token limits are model facts; cost is a compiled-in default
// that operator RoutingConfig may override.
//
// pricing as of 2026-07 — public AWS Bedrock on-demand USD per 1M tokens
// (us-east-1). Where an exact figure was uncertain a reasonable current value is
// used; operators can override via RoutingConfig.CostOverrides.
var bedrockCatalog = []genai.ModelInfo{
	// --- Anthropic Claude ---
	{
		Name:              "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Provider:          ProviderName,
		DisplayName:       "Claude 3.5 Sonnet v2",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapVision, genai.CapToolCalling, genai.CapJSONMode, genai.CapReasoning},
		MaxInputTokens:    200000,
		MaxOutputTokens:   8192,
		InputCostPerMTok:  3.0,
		OutputCostPerMTok: 15.0,
		Metadata:          map[string]string{"family": "claude"},
	},
	{
		Name:              "anthropic.claude-3-5-haiku-20241022-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Claude 3.5 Haiku",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapToolCalling, genai.CapJSONMode},
		MaxInputTokens:    200000,
		MaxOutputTokens:   8192,
		InputCostPerMTok:  0.8,
		OutputCostPerMTok: 4.0,
		Metadata:          map[string]string{"family": "claude"},
	},
	{
		Name:              "anthropic.claude-3-opus-20240229-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Claude 3 Opus",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapVision, genai.CapToolCalling, genai.CapJSONMode, genai.CapReasoning},
		MaxInputTokens:    200000,
		MaxOutputTokens:   4096,
		InputCostPerMTok:  15.0,
		OutputCostPerMTok: 75.0,
		Metadata:          map[string]string{"family": "claude"},
	},
	{
		Name:              "anthropic.claude-3-sonnet-20240229-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Claude 3 Sonnet",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapVision, genai.CapToolCalling, genai.CapJSONMode},
		MaxInputTokens:    200000,
		MaxOutputTokens:   4096,
		InputCostPerMTok:  3.0,
		OutputCostPerMTok: 15.0,
		Metadata:          map[string]string{"family": "claude"},
	},
	{
		Name:              "anthropic.claude-3-haiku-20240307-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Claude 3 Haiku",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapVision, genai.CapToolCalling, genai.CapJSONMode},
		MaxInputTokens:    200000,
		MaxOutputTokens:   4096,
		InputCostPerMTok:  0.25,
		OutputCostPerMTok: 1.25,
		Metadata:          map[string]string{"family": "claude"},
	},

	// --- Amazon Nova ---
	{
		Name:              "amazon.nova-pro-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Amazon Nova Pro",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapVision, genai.CapToolCalling},
		MaxInputTokens:    300000,
		MaxOutputTokens:   5120,
		InputCostPerMTok:  0.8,
		OutputCostPerMTok: 3.2,
		Metadata:          map[string]string{"family": "nova"},
	},
	{
		Name:              "amazon.nova-lite-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Amazon Nova Lite",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapVision, genai.CapToolCalling},
		MaxInputTokens:    300000,
		MaxOutputTokens:   5120,
		InputCostPerMTok:  0.06,
		OutputCostPerMTok: 0.24,
		Metadata:          map[string]string{"family": "nova"},
	},
	{
		Name:              "amazon.nova-micro-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Amazon Nova Micro",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapToolCalling},
		MaxInputTokens:    128000,
		MaxOutputTokens:   5120,
		InputCostPerMTok:  0.035,
		OutputCostPerMTok: 0.14,
		Metadata:          map[string]string{"family": "nova"},
	},

	// --- Amazon Titan ---
	{
		Name:              "amazon.titan-text-express-v1",
		Provider:          ProviderName,
		DisplayName:       "Titan Text Express",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat},
		MaxInputTokens:    8192,
		MaxOutputTokens:   8192,
		InputCostPerMTok:  0.2,
		OutputCostPerMTok: 0.6,
		Metadata:          map[string]string{"family": "titan"},
	},
	{
		Name:              "amazon.titan-text-premier-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Titan Text Premier",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat},
		MaxInputTokens:    32000,
		MaxOutputTokens:   3072,
		InputCostPerMTok:  0.5,
		OutputCostPerMTok: 1.5,
		Metadata:          map[string]string{"family": "titan"},
	},
	{
		Name:             "amazon.titan-embed-text-v2:0",
		Provider:         ProviderName,
		DisplayName:      "Titan Text Embeddings v2",
		Capabilities:     []genai.Capability{genai.CapEmbeddings},
		MaxInputTokens:   8192,
		InputCostPerMTok: 0.02,
		Metadata:         map[string]string{"family": "titan"},
	},
	{
		Name:             "amazon.titan-embed-text-v1",
		Provider:         ProviderName,
		DisplayName:      "Titan Text Embeddings v1",
		Capabilities:     []genai.Capability{genai.CapEmbeddings},
		MaxInputTokens:   8192,
		InputCostPerMTok: 0.1,
		Metadata:         map[string]string{"family": "titan"},
	},

	// --- Meta Llama ---
	{
		Name:              "meta.llama3-1-70b-instruct-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Llama 3.1 70B Instruct",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapToolCalling},
		MaxInputTokens:    128000,
		MaxOutputTokens:   4096,
		InputCostPerMTok:  0.72,
		OutputCostPerMTok: 0.72,
		Metadata:          map[string]string{"family": "llama"},
	},
	{
		Name:              "meta.llama3-1-8b-instruct-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Llama 3.1 8B Instruct",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapToolCalling},
		MaxInputTokens:    128000,
		MaxOutputTokens:   4096,
		InputCostPerMTok:  0.22,
		OutputCostPerMTok: 0.22,
		Metadata:          map[string]string{"family": "llama"},
	},

	// --- Mistral ---
	{
		Name:              "mistral.mistral-large-2407-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Mistral Large 2 (24.07)",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapToolCalling, genai.CapJSONMode},
		MaxInputTokens:    128000,
		MaxOutputTokens:   8192,
		InputCostPerMTok:  2.0,
		OutputCostPerMTok: 6.0,
		Metadata:          map[string]string{"family": "mistral"},
	},

	// --- Cohere ---
	{
		Name:              "cohere.command-r-plus-v1:0",
		Provider:          ProviderName,
		DisplayName:       "Cohere Command R+",
		Capabilities:      []genai.Capability{genai.CapText, genai.CapChat, genai.CapStreaming, genai.CapToolCalling},
		MaxInputTokens:    128000,
		MaxOutputTokens:   4096,
		InputCostPerMTok:  3.0,
		OutputCostPerMTok: 15.0,
		Metadata:          map[string]string{"family": "cohere"},
	},
	{
		Name:             "cohere.embed-english-v3",
		Provider:         ProviderName,
		DisplayName:      "Cohere Embed English v3",
		Capabilities:     []genai.Capability{genai.CapEmbeddings},
		MaxInputTokens:   512,
		InputCostPerMTok: 0.1,
		Metadata:         map[string]string{"family": "cohere"},
	},
	{
		Name:             "cohere.embed-multilingual-v3",
		Provider:         ProviderName,
		DisplayName:      "Cohere Embed Multilingual v3",
		Capabilities:     []genai.Capability{genai.CapEmbeddings},
		MaxInputTokens:   512,
		InputCostPerMTok: 0.1,
		Metadata:         map[string]string{"family": "cohere"},
	},
}

// ModelCatalog returns the authoritative capability metadata for every model the
// Bedrock provider serves. It returns a defensive copy — including copies of each
// entry's Capabilities and Metadata — so callers cannot mutate the package table.
func (p *BedrockProvider) ModelCatalog() []genai.ModelInfo {
	out := make([]genai.ModelInfo, len(bedrockCatalog))
	for i, m := range bedrockCatalog {
		out[i] = cloneModelInfo(m)
	}
	return out
}

// ModelInfoFor returns metadata for a single model id, if the Bedrock provider
// knows it. The returned value is a defensive copy.
func (p *BedrockProvider) ModelInfoFor(model string) (genai.ModelInfo, bool) {
	for _, m := range bedrockCatalog {
		if m.Name == model {
			return cloneModelInfo(m), true
		}
	}
	return genai.ModelInfo{}, false
}

// cloneModelInfo deep-copies the mutable reference fields (Capabilities slice and
// Metadata map) of a ModelInfo so the package-level table stays immutable.
func cloneModelInfo(m genai.ModelInfo) genai.ModelInfo {
	if m.Capabilities != nil {
		caps := make([]genai.Capability, len(m.Capabilities))
		copy(caps, m.Capabilities)
		m.Capabilities = caps
	}
	if m.Metadata != nil {
		meta := make(map[string]string, len(m.Metadata))
		for k, v := range m.Metadata {
			meta[k] = v
		}
		m.Metadata = meta
	}
	return m
}
