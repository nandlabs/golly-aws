package bedrock

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"oss.nandlabs.io/golly/data"
	"oss.nandlabs.io/golly/genai"
)

// --- Tool config translation tests ---

func makeWeatherTool() genai.Tool {
	loc := "string"
	return genai.Tool{
		Function: &genai.FunctionDecl{
			Name:        "get_weather",
			Description: "Return current weather for a location.",
			Parameters: &data.Schema{
				Type: data.SchemaTypeObject,
				Properties: map[string]*data.Schema{
					"location": {Type: loc, Description: "City name"},
				},
				Required: []string{"location"},
			},
		},
	}
}

func TestConverse_ToolsPassedThrough(t *testing.T) {
	tool := makeWeatherTool()
	options := genai.NewOptionsBuilder().SetTools(tool).Build()

	var captured *bedrockruntime.ConverseInput
	mock := &mockConverseAPI{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			captured = params
			return &bedrockruntime.ConverseOutput{
				Output: &brtypes.ConverseOutputMemberMessage{
					Value: brtypes.Message{Role: brtypes.ConversationRoleAssistant},
				},
				StopReason: brtypes.StopReasonEndTurn,
			}, nil
		},
	}
	provider := &BedrockProvider{client: mock, models: []string{"test"}}

	_, err := provider.Generate(context.Background(), "test", genai.NewTextMessage(genai.RoleUser, "hi"), options)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if captured == nil || captured.ToolConfig == nil {
		t.Fatal("expected ToolConfig on captured ConverseInput")
	}
	if len(captured.ToolConfig.Tools) != 1 {
		t.Fatalf("ToolConfig.Tools length = %d, want 1", len(captured.ToolConfig.Tools))
	}
	spec, ok := captured.ToolConfig.Tools[0].(*brtypes.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("Tools[0] = %T, want *ToolMemberToolSpec", captured.ToolConfig.Tools[0])
	}
	if spec.Value.Name == nil || *spec.Value.Name != "get_weather" {
		t.Errorf("tool name = %v, want get_weather", spec.Value.Name)
	}
	if spec.Value.Description == nil || *spec.Value.Description == "" {
		t.Error("expected non-empty tool description")
	}
	inputSchema, ok := spec.Value.InputSchema.(*brtypes.ToolInputSchemaMemberJson)
	if !ok {
		t.Fatalf("InputSchema = %T, want *ToolInputSchemaMemberJson", spec.Value.InputSchema)
	}
	if inputSchema.Value == nil {
		t.Fatal("InputSchema.Value document is nil")
	}
	// The document should marshal to JSON containing type=object and a properties map.
	raw, err := inputSchema.Value.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("MarshalSmithyDocument: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", raw, err)
	}
	if got["type"] != "object" {
		t.Errorf("schema type = %v, want object", got["type"])
	}
	if _, ok := got["properties"]; !ok {
		t.Errorf("expected properties field in schema, got %v", got)
	}
}

func TestConverse_ToolChoice_Auto(t *testing.T) {
	options := genai.NewOptionsBuilder().
		SetTools(makeWeatherTool()).
		SetToolChoice(genai.NewToolChoice(genai.ToolChoiceAuto)).
		Build()

	cfg, err := buildToolConfig(options)
	if err != nil {
		t.Fatalf("buildToolConfig error: %v", err)
	}
	if cfg == nil || cfg.ToolChoice == nil {
		t.Fatal("expected ToolConfig with ToolChoice set")
	}
	if _, ok := cfg.ToolChoice.(*brtypes.ToolChoiceMemberAuto); !ok {
		t.Errorf("ToolChoice = %T, want *ToolChoiceMemberAuto", cfg.ToolChoice)
	}
}

func TestConverse_ToolChoice_Any(t *testing.T) {
	options := genai.NewOptionsBuilder().
		SetTools(makeWeatherTool()).
		SetToolChoice(genai.NewToolChoice(genai.ToolChoiceRequired)).
		Build()

	cfg, err := buildToolConfig(options)
	if err != nil {
		t.Fatalf("buildToolConfig error: %v", err)
	}
	if _, ok := cfg.ToolChoice.(*brtypes.ToolChoiceMemberAny); !ok {
		t.Errorf("ToolChoice = %T, want *ToolChoiceMemberAny", cfg.ToolChoice)
	}
}

func TestConverse_ToolChoice_Named(t *testing.T) {
	options := genai.NewOptionsBuilder().
		SetTools(makeWeatherTool()).
		SetToolChoice(genai.NewNamedToolChoice("get_weather")).
		Build()

	cfg, err := buildToolConfig(options)
	if err != nil {
		t.Fatalf("buildToolConfig error: %v", err)
	}
	named, ok := cfg.ToolChoice.(*brtypes.ToolChoiceMemberTool)
	if !ok {
		t.Fatalf("ToolChoice = %T, want *ToolChoiceMemberTool", cfg.ToolChoice)
	}
	if named.Value.Name == nil || *named.Value.Name != "get_weather" {
		t.Errorf("named tool = %v, want get_weather", named.Value.Name)
	}
}

func TestConverse_ToolChoice_None(t *testing.T) {
	options := genai.NewOptionsBuilder().
		SetTools(makeWeatherTool()).
		SetToolChoice(genai.NewToolChoice(genai.ToolChoiceNone)).
		Build()

	cfg, err := buildToolConfig(options)
	if err != nil {
		t.Fatalf("buildToolConfig error: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil ToolConfig for ToolChoiceNone, got %+v", cfg)
	}

	// End-to-end: Generate should not populate ToolConfig on the input.
	var captured *bedrockruntime.ConverseInput
	mock := &mockConverseAPI{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			captured = params
			return &bedrockruntime.ConverseOutput{
				Output:     &brtypes.ConverseOutputMemberMessage{Value: brtypes.Message{Role: brtypes.ConversationRoleAssistant}},
				StopReason: brtypes.StopReasonEndTurn,
			}, nil
		},
	}
	provider := &BedrockProvider{client: mock, models: []string{"test"}}
	if _, err := provider.Generate(context.Background(), "test", genai.NewTextMessage(genai.RoleUser, "hi"), options); err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected converse to be called")
	}
	if captured.ToolConfig != nil {
		t.Errorf("expected nil ToolConfig on ConverseInput for None, got %+v", captured.ToolConfig)
	}
}

func TestConverse_NamedToolChoice_MissingName(t *testing.T) {
	options := genai.NewOptionsBuilder().
		SetTools(makeWeatherTool()).
		SetToolChoice(&genai.ToolChoice{Mode: genai.ToolChoiceNamed}).
		Build()
	if _, err := buildToolConfig(options); err == nil {
		t.Error("expected error for named tool choice with empty name")
	}
}

func TestConverse_NoTools_NoToolConfig(t *testing.T) {
	options := genai.NewOptionsBuilder().Build()
	cfg, err := buildToolConfig(options)
	if err != nil {
		t.Fatalf("buildToolConfig error: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil ToolConfig when no tools declared, got %+v", cfg)
	}
}

func TestConverse_Stream_ToolsPassedThrough(t *testing.T) {
	options := genai.NewOptionsBuilder().SetTools(makeWeatherTool()).Build()
	input, err := buildConverseStreamInput("test-model", genai.NewTextMessage(genai.RoleUser, "hi"), options)
	if err != nil {
		t.Fatalf("buildConverseStreamInput error: %v", err)
	}
	if input.ToolConfig == nil || len(input.ToolConfig.Tools) != 1 {
		t.Errorf("expected ToolConfig with 1 tool on stream input")
	}
}

// --- Embedder tests ---

func TestEmbed_Titan_SingleInput(t *testing.T) {
	want := []float32{0.1, 0.2, 0.3}
	respBody, _ := json.Marshal(titanEmbedResponse{Embedding: want, InputTextTokenCount: 3})

	var captured *bedrockruntime.InvokeModelInput
	mock := &mockConverseAPI{
		invokeModelFunc: func(ctx context.Context, params *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
			captured = params
			return &bedrockruntime.InvokeModelOutput{Body: respBody}, nil
		},
	}
	provider := &BedrockProvider{client: mock}
	resp, err := provider.Embed(context.Background(), &genai.EmbedRequest{
		Model:  "amazon.titan-embed-text-v2:0",
		Inputs: []genai.Part{{Text: &genai.TextPart{Content: "hello"}}},
	})
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if len(resp.Vectors) != 1 {
		t.Fatalf("Vectors length = %d, want 1", len(resp.Vectors))
	}
	if !float32SliceEq(resp.Vectors[0], want) {
		t.Errorf("Vectors[0] = %v, want %v", resp.Vectors[0], want)
	}
	if resp.Meta == nil || resp.Meta.InputTokens != 3 {
		t.Errorf("Meta.InputTokens = %v, want 3", resp.Meta)
	}
	// Verify request body was Titan-shaped.
	if captured == nil {
		t.Fatal("expected InvokeModel to be called")
	}
	var reqBody titanEmbedRequest
	if err := json.Unmarshal(captured.Body, &reqBody); err != nil {
		t.Fatalf("unmarshal captured request: %v", err)
	}
	if reqBody.InputText != "hello" {
		t.Errorf("inputText = %q, want hello", reqBody.InputText)
	}
}

func TestEmbed_Titan_MultipleInputs(t *testing.T) {
	callCount := 0
	mock := &mockConverseAPI{
		invokeModelFunc: func(ctx context.Context, params *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
			callCount++
			body, _ := json.Marshal(titanEmbedResponse{
				Embedding:           []float32{float32(callCount), 0.0},
				InputTextTokenCount: callCount,
			})
			return &bedrockruntime.InvokeModelOutput{Body: body}, nil
		},
	}
	provider := &BedrockProvider{client: mock}
	resp, err := provider.Embed(context.Background(), &genai.EmbedRequest{
		Model: "amazon.titan-embed-text-v2:0",
		Inputs: []genai.Part{
			{Text: &genai.TextPart{Content: "one"}},
			{Text: &genai.TextPart{Content: "two"}},
			{Text: &genai.TextPart{Content: "three"}},
		},
	})
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("InvokeModel called %d times, want 3", callCount)
	}
	if len(resp.Vectors) != 3 {
		t.Fatalf("Vectors length = %d, want 3", len(resp.Vectors))
	}
	if resp.Meta.InputTokens != 6 { // 1+2+3
		t.Errorf("Meta.InputTokens = %d, want 6", resp.Meta.InputTokens)
	}
}

func TestEmbed_Cohere_BatchRequest(t *testing.T) {
	want := [][]float32{{0.1, 0.2}, {0.3, 0.4}}
	respBody, _ := json.Marshal(cohereEmbedResponse{Embeddings: want, ID: "req-1"})

	callCount := 0
	var captured *bedrockruntime.InvokeModelInput
	mock := &mockConverseAPI{
		invokeModelFunc: func(ctx context.Context, params *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
			callCount++
			captured = params
			return &bedrockruntime.InvokeModelOutput{Body: respBody}, nil
		},
	}
	provider := &BedrockProvider{client: mock}
	resp, err := provider.Embed(context.Background(), &genai.EmbedRequest{
		Model: "cohere.embed-english-v3",
		Inputs: []genai.Part{
			{Text: &genai.TextPart{Content: "alpha"}},
			{Text: &genai.TextPart{Content: "beta"}},
		},
	})
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("InvokeModel called %d times, want 1 (batch native)", callCount)
	}
	if len(resp.Vectors) != 2 {
		t.Fatalf("Vectors length = %d, want 2", len(resp.Vectors))
	}
	if !float32SliceEq(resp.Vectors[0], want[0]) || !float32SliceEq(resp.Vectors[1], want[1]) {
		t.Errorf("Vectors = %v, want %v", resp.Vectors, want)
	}
	// Request body sanity: texts array and default input_type.
	var reqBody cohereEmbedRequest
	if err := json.Unmarshal(captured.Body, &reqBody); err != nil {
		t.Fatalf("unmarshal captured request: %v", err)
	}
	if len(reqBody.Texts) != 2 || reqBody.Texts[0] != "alpha" || reqBody.Texts[1] != "beta" {
		t.Errorf("texts = %v, want [alpha beta]", reqBody.Texts)
	}
	if reqBody.InputType != cohereDefaultInputType {
		t.Errorf("input_type = %q, want %q", reqBody.InputType, cohereDefaultInputType)
	}
}

func TestEmbed_Cohere_CustomInputType(t *testing.T) {
	respBody, _ := json.Marshal(cohereEmbedResponse{Embeddings: [][]float32{{0.5}}})
	var captured *bedrockruntime.InvokeModelInput
	mock := &mockConverseAPI{
		invokeModelFunc: func(ctx context.Context, params *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
			captured = params
			return &bedrockruntime.InvokeModelOutput{Body: respBody}, nil
		},
	}
	provider := &BedrockProvider{client: mock}
	_, err := provider.Embed(context.Background(), &genai.EmbedRequest{
		Model: "cohere.embed-multilingual-v3",
		Inputs: []genai.Part{
			{
				Text:       &genai.TextPart{Content: "q"},
				Attributes: map[string]interface{}{"input_type": "search_query"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	var reqBody cohereEmbedRequest
	if err := json.Unmarshal(captured.Body, &reqBody); err != nil {
		t.Fatalf("unmarshal captured request: %v", err)
	}
	if reqBody.InputType != "search_query" {
		t.Errorf("input_type = %q, want search_query", reqBody.InputType)
	}
}

func TestEmbed_UnknownModel_Errors(t *testing.T) {
	provider := &BedrockProvider{client: &mockConverseAPI{}}
	_, err := provider.Embed(context.Background(), &genai.EmbedRequest{
		Model:  "meta.llama3-8b-instruct",
		Inputs: []genai.Part{{Text: &genai.TextPart{Content: "x"}}},
	})
	if err == nil {
		t.Fatal("expected error for unknown embed model prefix")
	}
	if !contains(err.Error(), "unsupported embed model") {
		t.Errorf("error = %q, expected to contain 'unsupported embed model'", err.Error())
	}
}

func TestEmbed_NilRequest_Errors(t *testing.T) {
	provider := &BedrockProvider{client: &mockConverseAPI{}}
	if _, err := provider.Embed(context.Background(), nil); err == nil {
		t.Error("expected error for nil request")
	}
}

func TestEmbed_MissingModel_Errors(t *testing.T) {
	provider := &BedrockProvider{client: &mockConverseAPI{}}
	_, err := provider.Embed(context.Background(), &genai.EmbedRequest{
		Inputs: []genai.Part{{Text: &genai.TextPart{Content: "x"}}},
	})
	if err == nil {
		t.Error("expected error for missing model id")
	}
}

func TestEmbed_NoInputs_Errors(t *testing.T) {
	provider := &BedrockProvider{client: &mockConverseAPI{}}
	_, err := provider.Embed(context.Background(), &genai.EmbedRequest{
		Model: "amazon.titan-embed-text-v2:0",
	})
	if err == nil {
		t.Error("expected error for empty inputs")
	}
}

func TestEmbed_NonTextInput_Errors(t *testing.T) {
	provider := &BedrockProvider{client: &mockConverseAPI{}}
	_, err := provider.Embed(context.Background(), &genai.EmbedRequest{
		Model:  "amazon.titan-embed-text-v2:0",
		Inputs: []genai.Part{{Bin: &genai.BinPart{Data: []byte{1, 2, 3}}}},
	})
	if err == nil {
		t.Error("expected error for non-text input")
	}
}

func TestEmbed_TitanInvokeError(t *testing.T) {
	mock := &mockConverseAPI{
		invokeModelFunc: func(ctx context.Context, params *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
			return nil, fmt.Errorf("throttled")
		},
	}
	provider := &BedrockProvider{client: mock}
	_, err := provider.Embed(context.Background(), &genai.EmbedRequest{
		Model:  "amazon.titan-embed-text-v2:0",
		Inputs: []genai.Part{{Text: &genai.TextPart{Content: "hi"}}},
	})
	if err == nil {
		t.Fatal("expected error to propagate from InvokeModel")
	}
	if !contains(err.Error(), "throttled") {
		t.Errorf("error = %q, expected to contain 'throttled'", err.Error())
	}
}

// --- helpers ---

func float32SliceEq(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
