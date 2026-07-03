package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"oss.nandlabs.io/golly/genai"
)

// Model-family prefixes recognised by the Bedrock Embedder.
const (
	amazonModelPrefix = "amazon."
	cohereModelPrefix = "cohere."
)

// Default Cohere input_type when the caller has not supplied one via
// Part.Attributes["input_type"]. Cohere v3 embed models require an input_type.
const cohereDefaultInputType = "search_document"

// Embed generates embedding vectors for the supplied inputs using AWS Bedrock's
// InvokeModel API (Converse does not cover embeddings). Model family is detected
// from EmbedRequest.Model:
//
//   - "amazon.titan-embed-*"  — Titan-family bodies. Titan accepts a single
//     inputText per invocation, so multi-input requests fan out to one
//     InvokeModel call per input.
//   - "cohere.embed-*"       — Cohere-family bodies. Batch-native: a single
//     InvokeModel call carries all inputs.
//
// Unknown model prefixes return an error.
func (p *BedrockProvider) Embed(ctx context.Context, req *genai.EmbedRequest) (*genai.EmbedResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("embed request is nil")
	}
	if req.Model == "" {
		return nil, fmt.Errorf("embed request requires a Model id")
	}
	if len(req.Inputs) == 0 {
		return nil, fmt.Errorf("embed request has no inputs")
	}

	texts, err := embedInputsAsText(req.Inputs)
	if err != nil {
		return nil, err
	}

	model := strings.ToLower(req.Model)
	switch {
	case strings.HasPrefix(model, amazonModelPrefix):
		return p.embedTitan(ctx, req.Model, texts)
	case strings.HasPrefix(model, cohereModelPrefix):
		return p.embedCohere(ctx, req.Model, texts, cohereInputType(req.Inputs))
	default:
		return nil, fmt.Errorf("unsupported embed model %q: expected prefix %q or %q",
			req.Model, amazonModelPrefix, cohereModelPrefix)
	}
}

// embedInputsAsText extracts a text string per Part. Only text parts are supported
// today; binary / file parts return an error rather than being silently dropped.
func embedInputsAsText(inputs []genai.Part) ([]string, error) {
	texts := make([]string, 0, len(inputs))
	for i, part := range inputs {
		if part.Text == nil || part.Text.Content == "" {
			return nil, fmt.Errorf("embed input[%d]: only non-empty text parts are supported", i)
		}
		texts = append(texts, part.Text.Content)
	}
	return texts, nil
}

// cohereInputType reads an optional "input_type" attribute from the first input.
// Cohere embed v3 requires an input_type; we default to "search_document" when
// no override is supplied.
func cohereInputType(inputs []genai.Part) string {
	if len(inputs) == 0 {
		return cohereDefaultInputType
	}
	if v, ok := inputs[0].Attributes["input_type"].(string); ok && v != "" {
		return v
	}
	return cohereDefaultInputType
}

// --- Titan ---

// titanEmbedRequest is the Bedrock request body for amazon.titan-embed-text models.
type titanEmbedRequest struct {
	InputText string `json:"inputText"`
}

// titanEmbedResponse is the Bedrock response body for amazon.titan-embed-text models.
type titanEmbedResponse struct {
	Embedding           []float32 `json:"embedding"`
	InputTextTokenCount int       `json:"inputTextTokenCount"`
}

// embedTitan invokes Titan once per input (Titan accepts a single inputText per
// call) and stitches the results into an EmbedResponse.
func (p *BedrockProvider) embedTitan(ctx context.Context, model string, texts []string) (*genai.EmbedResponse, error) {
	vectors := make([][]float32, 0, len(texts))
	totalTokens := 0

	for i, text := range texts {
		body, err := json.Marshal(titanEmbedRequest{InputText: text})
		if err != nil {
			return nil, fmt.Errorf("marshal titan request[%d]: %w", i, err)
		}
		out, err := p.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
			ModelId:     aws.String(model),
			ContentType: aws.String("application/json"),
			Accept:      aws.String("application/json"),
			Body:        body,
		})
		if err != nil {
			return nil, fmt.Errorf("bedrock InvokeModel (titan embed) failed for input[%d]: %w", i, err)
		}
		var resp titanEmbedResponse
		if err := json.Unmarshal(out.Body, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal titan response[%d]: %w", i, err)
		}
		vectors = append(vectors, resp.Embedding)
		totalTokens += resp.InputTextTokenCount
	}

	return &genai.EmbedResponse{
		Vectors: vectors,
		Meta: &genai.ResponseMeta{
			InputTokens: totalTokens,
			TotalTokens: totalTokens,
		},
	}, nil
}

// --- Cohere ---

// cohereEmbedRequest is the Bedrock request body for cohere.embed-* models.
type cohereEmbedRequest struct {
	Texts     []string `json:"texts"`
	InputType string   `json:"input_type"`
}

// cohereEmbedResponse is the Bedrock response body for cohere.embed-* models.
type cohereEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	ID         string      `json:"id,omitempty"`
}

// embedCohere batches all inputs into a single InvokeModel call — Cohere's
// embed API is batch-native.
func (p *BedrockProvider) embedCohere(ctx context.Context, model string, texts []string, inputType string) (*genai.EmbedResponse, error) {
	body, err := json.Marshal(cohereEmbedRequest{Texts: texts, InputType: inputType})
	if err != nil {
		return nil, fmt.Errorf("marshal cohere request: %w", err)
	}
	out, err := p.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(model),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock InvokeModel (cohere embed) failed: %w", err)
	}
	var resp cohereEmbedResponse
	if err := json.Unmarshal(out.Body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal cohere response: %w", err)
	}
	// Cohere doesn't report token counts on the embed endpoint — leave Meta empty.
	return &genai.EmbedResponse{
		Vectors: resp.Embeddings,
		Meta:    &genai.ResponseMeta{},
	}, nil
}
