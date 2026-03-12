package bedrock

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"oss.nandlabs.io/golly/genai"
)

// --- Mock client ---

type mockConverseAPI struct {
	converseFunc       func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
	converseStreamFunc func(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

func (m *mockConverseAPI) Converse(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	if m.converseFunc != nil {
		return m.converseFunc(ctx, params, optFns...)
	}
	return nil, fmt.Errorf("converse not implemented")
}

func (m *mockConverseAPI) ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	if m.converseStreamFunc != nil {
		return m.converseStreamFunc(ctx, params, optFns...)
	}
	return nil, fmt.Errorf("converseStream not implemented")
}

// --- Provider metadata tests ---

func TestProviderMetadata(t *testing.T) {
	p := &BedrockProvider{
		client:      &mockConverseAPI{},
		models:      []string{"anthropic.claude-3-sonnet", "amazon.titan-text-premier-v1:0"},
		description: ProviderDescription,
		version:     ProviderVersion,
	}

	if p.Name() != ProviderName {
		t.Errorf("Name() = %q, want %q", p.Name(), ProviderName)
	}
	if p.Description() != ProviderDescription {
		t.Errorf("Description() = %q, want %q", p.Description(), ProviderDescription)
	}
	if p.Version() != ProviderVersion {
		t.Errorf("Version() = %q, want %q", p.Version(), ProviderVersion)
	}
	if len(p.Models()) != 2 {
		t.Errorf("Models() returned %d models, want 2", len(p.Models()))
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

// --- Role conversion tests ---

func TestConvertRole(t *testing.T) {
	tests := []struct {
		role genai.Role
		want brtypes.ConversationRole
	}{
		{genai.RoleUser, brtypes.ConversationRoleUser},
		{genai.RoleAssistant, brtypes.ConversationRoleAssistant},
		{genai.RoleSystem, brtypes.ConversationRoleUser}, // system defaults to user
	}
	for _, tt := range tests {
		got := convertRole(tt.role)
		if got != tt.want {
			t.Errorf("convertRole(%v) = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestConvertFromBedrockRole(t *testing.T) {
	tests := []struct {
		role brtypes.ConversationRole
		want genai.Role
	}{
		{brtypes.ConversationRoleUser, genai.RoleUser},
		{brtypes.ConversationRoleAssistant, genai.RoleAssistant},
		{"unknown", genai.RoleAssistant}, // defaults to assistant
	}
	for _, tt := range tests {
		got := convertFromBedrockRole(tt.role)
		if got != tt.want {
			t.Errorf("convertFromBedrockRole(%v) = %v, want %v", tt.role, got, tt.want)
		}
	}
}

// --- Stop reason mapping tests ---

func TestMapStopReason(t *testing.T) {
	tests := []struct {
		reason brtypes.StopReason
		want   genai.FinishReason
	}{
		{brtypes.StopReasonEndTurn, genai.FinishReasonEndTurn},
		{brtypes.StopReasonMaxTokens, genai.FinishReasonLength},
		{brtypes.StopReasonStopSequence, genai.FinishReasonStop},
		{brtypes.StopReasonToolUse, genai.FinishReasonToolCall},
		{brtypes.StopReasonContentFiltered, genai.FinishReasonContentFilter},
		{"unknown_reason", genai.FinishReasonUnknown},
	}
	for _, tt := range tests {
		got := mapStopReason(tt.reason)
		if got != tt.want {
			t.Errorf("mapStopReason(%v) = %v, want %v", tt.reason, got, tt.want)
		}
	}
}

// --- MIME conversion tests ---

func TestMimeToImageFormat(t *testing.T) {
	tests := []struct {
		mime    string
		want    brtypes.ImageFormat
		wantErr bool
	}{
		{"image/png", brtypes.ImageFormatPng, false},
		{"image/jpeg", brtypes.ImageFormatJpeg, false},
		{"image/jpg", brtypes.ImageFormatJpeg, false},
		{"image/gif", brtypes.ImageFormatGif, false},
		{"image/webp", brtypes.ImageFormatWebp, false},
		{"image/bmp", "", true},
	}
	for _, tt := range tests {
		got, err := mimeToImageFormat(tt.mime)
		if (err != nil) != tt.wantErr {
			t.Errorf("mimeToImageFormat(%q): err = %v, wantErr = %v", tt.mime, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("mimeToImageFormat(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}

func TestImageFormatToMime(t *testing.T) {
	tests := []struct {
		format brtypes.ImageFormat
		want   string
	}{
		{brtypes.ImageFormatPng, "image/png"},
		{brtypes.ImageFormatJpeg, "image/jpeg"},
		{brtypes.ImageFormatGif, "image/gif"},
		{brtypes.ImageFormatWebp, "image/webp"},
		{"unknown", "application/octet-stream"},
	}
	for _, tt := range tests {
		got := imageFormatToMime(tt.format)
		if got != tt.want {
			t.Errorf("imageFormatToMime(%v) = %q, want %q", tt.format, got, tt.want)
		}
	}
}

func TestMimeToDocumentFormat(t *testing.T) {
	tests := []struct {
		mime    string
		want    brtypes.DocumentFormat
		wantErr bool
	}{
		{"application/pdf", brtypes.DocumentFormatPdf, false},
		{"text/csv", brtypes.DocumentFormatCsv, false},
		{"application/msword", brtypes.DocumentFormatDoc, false},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", brtypes.DocumentFormatDocx, false},
		{"application/vnd.ms-excel", brtypes.DocumentFormatXls, false},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", brtypes.DocumentFormatXlsx, false},
		{"text/html", brtypes.DocumentFormatHtml, false},
		{"text/plain", brtypes.DocumentFormatTxt, false},
		{"text/markdown", brtypes.DocumentFormatMd, false},
		{"application/octet-stream", "", true},
	}
	for _, tt := range tests {
		got, err := mimeToDocumentFormat(tt.mime)
		if (err != nil) != tt.wantErr {
			t.Errorf("mimeToDocumentFormat(%q): err = %v, wantErr = %v", tt.mime, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("mimeToDocumentFormat(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}

// --- System content extraction tests ---

func TestExtractSystemContent_FromOptions(t *testing.T) {
	options := genai.NewOptionsBuilder().
		Add(genai.OptionSystemInstructions, "You are a helpful assistant.").
		Build()
	blocks := extractSystemContent(nil, options)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 system block, got %d", len(blocks))
	}
	textBlock, ok := blocks[0].(*brtypes.SystemContentBlockMemberText)
	if !ok {
		t.Fatal("expected SystemContentBlockMemberText")
	}
	if textBlock.Value != "You are a helpful assistant." {
		t.Errorf("system text = %q, want %q", textBlock.Value, "You are a helpful assistant.")
	}
}

func TestExtractSystemContent_FromMessage(t *testing.T) {
	msg := genai.NewTextMessage(genai.RoleSystem, "System prompt text")
	blocks := extractSystemContent(msg, nil)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 system block, got %d", len(blocks))
	}
	textBlock, ok := blocks[0].(*brtypes.SystemContentBlockMemberText)
	if !ok {
		t.Fatal("expected SystemContentBlockMemberText")
	}
	if textBlock.Value != "System prompt text" {
		t.Errorf("system text = %q, want %q", textBlock.Value, "System prompt text")
	}
}

func TestExtractSystemContent_Combined(t *testing.T) {
	msg := genai.NewTextMessage(genai.RoleSystem, "From message")
	options := genai.NewOptionsBuilder().
		Add(genai.OptionSystemInstructions, "From options").
		Build()
	blocks := extractSystemContent(msg, options)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 system blocks, got %d", len(blocks))
	}
}

func TestExtractSystemContent_NilInputs(t *testing.T) {
	blocks := extractSystemContent(nil, nil)
	if len(blocks) != 0 {
		t.Errorf("expected 0 system blocks, got %d", len(blocks))
	}
}

// --- Inference config tests ---

func TestBuildInferenceConfig_AllOptions(t *testing.T) {
	options := genai.NewOptionsBuilder().
		SetMaxTokens(2048).
		SetTemperature(0.5).
		SetTopP(0.9).
		SetStopWords("STOP", "END").
		Build()
	cfg := buildInferenceConfig(options)
	if cfg == nil {
		t.Fatal("expected non-nil InferenceConfiguration")
	}
	if *cfg.MaxTokens != 2048 {
		t.Errorf("MaxTokens = %d, want 2048", *cfg.MaxTokens)
	}
	if *cfg.Temperature != 0.5 {
		t.Errorf("Temperature = %f, want 0.5", *cfg.Temperature)
	}
	if len(cfg.StopSequences) != 2 {
		t.Errorf("StopSequences length = %d, want 2", len(cfg.StopSequences))
	}
}

func TestBuildInferenceConfig_NoOptions(t *testing.T) {
	options := genai.NewOptionsBuilder().Build()
	cfg := buildInferenceConfig(options)
	if cfg != nil {
		t.Error("expected nil InferenceConfiguration for empty options")
	}
}

// --- Message conversion tests ---

func TestConvertMessage_TextPart(t *testing.T) {
	msg := genai.NewTextMessage(genai.RoleUser, "Hello, world!")
	brMsg, err := convertMessage(msg)
	if err != nil {
		t.Fatalf("convertMessage error: %v", err)
	}
	if brMsg.Role != brtypes.ConversationRoleUser {
		t.Errorf("Role = %v, want user", brMsg.Role)
	}
	if len(brMsg.Content) != 1 {
		t.Fatalf("Content length = %d, want 1", len(brMsg.Content))
	}
	textBlock, ok := brMsg.Content[0].(*brtypes.ContentBlockMemberText)
	if !ok {
		t.Fatal("expected ContentBlockMemberText")
	}
	if textBlock.Value != "Hello, world!" {
		t.Errorf("text = %q, want %q", textBlock.Value, "Hello, world!")
	}
}

func TestConvertMessage_EmptyParts(t *testing.T) {
	msg := &genai.Message{Role: genai.RoleUser, Parts: []genai.Part{}}
	brMsg, err := convertMessage(msg)
	if err != nil {
		t.Fatalf("convertMessage error: %v", err)
	}
	// Should have at least one content block (empty text fallback)
	if len(brMsg.Content) != 1 {
		t.Errorf("Content length = %d, want 1", len(brMsg.Content))
	}
}

// --- Part conversion tests ---

func TestConvertPart_Image(t *testing.T) {
	part := &genai.Part{
		Name:     "photo",
		MimeType: "image/png",
		Bin:      &genai.BinPart{Data: []byte{0x89, 0x50, 0x4E, 0x47}},
	}
	block, err := convertPart(part)
	if err != nil {
		t.Fatalf("convertPart error: %v", err)
	}
	imgBlock, ok := block.(*brtypes.ContentBlockMemberImage)
	if !ok {
		t.Fatal("expected ContentBlockMemberImage")
	}
	if imgBlock.Value.Format != brtypes.ImageFormatPng {
		t.Errorf("Format = %v, want png", imgBlock.Value.Format)
	}
}

func TestConvertPart_Document(t *testing.T) {
	part := &genai.Part{
		Name:     "report",
		MimeType: "application/pdf",
		Bin:      &genai.BinPart{Data: []byte("%PDF-1.4")},
	}
	block, err := convertPart(part)
	if err != nil {
		t.Fatalf("convertPart error: %v", err)
	}
	docBlock, ok := block.(*brtypes.ContentBlockMemberDocument)
	if !ok {
		t.Fatal("expected ContentBlockMemberDocument")
	}
	if docBlock.Value.Format != brtypes.DocumentFormatPdf {
		t.Errorf("Format = %v, want pdf", docBlock.Value.Format)
	}
	if *docBlock.Value.Name != "report" {
		t.Errorf("Name = %q, want %q", *docBlock.Value.Name, "report")
	}
}

func TestConvertPart_UnsupportedBinary(t *testing.T) {
	part := &genai.Part{
		Name:     "data",
		MimeType: "application/octet-stream",
		Bin:      &genai.BinPart{Data: []byte{0x01, 0x02, 0x03}},
	}
	block, err := convertPart(part)
	if err != nil {
		t.Fatalf("convertPart error: %v", err)
	}
	// Should fallback to base64 text
	textBlock, ok := block.(*brtypes.ContentBlockMemberText)
	if !ok {
		t.Fatal("expected ContentBlockMemberText fallback")
	}
	if textBlock.Value == "" {
		t.Error("expected non-empty base64 text")
	}
}

func TestConvertPart_FuncCall(t *testing.T) {
	part := &genai.Part{
		Name: "get_weather",
		FuncCall: &genai.FuncCallPart{
			Id:           "call_123",
			FunctionName: "get_weather",
			Arguments:    map[string]interface{}{"location": "Seattle"},
		},
	}
	block, err := convertPart(part)
	if err != nil {
		t.Fatalf("convertPart error: %v", err)
	}
	toolBlock, ok := block.(*brtypes.ContentBlockMemberToolUse)
	if !ok {
		t.Fatal("expected ContentBlockMemberToolUse")
	}
	if *toolBlock.Value.ToolUseId != "call_123" {
		t.Errorf("ToolUseId = %q, want %q", *toolBlock.Value.ToolUseId, "call_123")
	}
	if *toolBlock.Value.Name != "get_weather" {
		t.Errorf("Name = %q, want %q", *toolBlock.Value.Name, "get_weather")
	}
}

func TestConvertPart_FuncResponse(t *testing.T) {
	text := "72°F and sunny"
	part := &genai.Part{
		Name: "call_123",
		FuncResponse: &genai.FuncResponsePart{
			Text: &text,
		},
	}
	block, err := convertPart(part)
	if err != nil {
		t.Fatalf("convertPart error: %v", err)
	}
	resultBlock, ok := block.(*brtypes.ContentBlockMemberToolResult)
	if !ok {
		t.Fatal("expected ContentBlockMemberToolResult")
	}
	if *resultBlock.Value.ToolUseId != "call_123" {
		t.Errorf("ToolUseId = %q, want %q", *resultBlock.Value.ToolUseId, "call_123")
	}
	if len(resultBlock.Value.Content) != 1 {
		t.Fatalf("Content length = %d, want 1", len(resultBlock.Value.Content))
	}
}

func TestConvertPart_NilPart(t *testing.T) {
	part := &genai.Part{Name: "empty"}
	block, err := convertPart(part)
	if err != nil {
		t.Fatalf("convertPart error: %v", err)
	}
	if block != nil {
		t.Error("expected nil block for empty part")
	}
}

// --- Response conversion tests ---

func TestToGenResponse(t *testing.T) {
	inputTokens := int32(100)
	outputTokens := int32(50)
	totalTokens := int32(150)
	latencyMs := int64(250)

	output := &bedrockruntime.ConverseOutput{
		Output: &brtypes.ConverseOutputMemberMessage{
			Value: brtypes.Message{
				Role: brtypes.ConversationRoleAssistant,
				Content: []brtypes.ContentBlock{
					&brtypes.ContentBlockMemberText{Value: "Hello! How can I help?"},
				},
			},
		},
		StopReason: brtypes.StopReasonEndTurn,
		Usage: &brtypes.TokenUsage{
			InputTokens:  &inputTokens,
			OutputTokens: &outputTokens,
			TotalTokens:  &totalTokens,
		},
		Metrics: &brtypes.ConverseMetrics{
			LatencyMs: &latencyMs,
		},
	}

	genResp := toGenResponse(output)
	if genResp.Meta.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", genResp.Meta.InputTokens)
	}
	if genResp.Meta.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", genResp.Meta.OutputTokens)
	}
	if genResp.Meta.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", genResp.Meta.TotalTokens)
	}
	if genResp.Meta.TotalTime != 250 {
		t.Errorf("TotalTime = %d, want 250", genResp.Meta.TotalTime)
	}
	if len(genResp.Candidates) != 1 {
		t.Fatalf("Candidates length = %d, want 1", len(genResp.Candidates))
	}
	if genResp.Candidates[0].FinishReason != genai.FinishReasonEndTurn {
		t.Errorf("FinishReason = %v, want end_turn", genResp.Candidates[0].FinishReason)
	}
	if genResp.Candidates[0].Message == nil {
		t.Fatal("expected non-nil message")
	}
	if len(genResp.Candidates[0].Message.Parts) != 1 {
		t.Fatalf("Parts length = %d, want 1", len(genResp.Candidates[0].Message.Parts))
	}
	if genResp.Candidates[0].Message.Parts[0].Text.Content != "Hello! How can I help?" {
		t.Errorf("text = %q, want %q", genResp.Candidates[0].Message.Parts[0].Text.Content, "Hello! How can I help?")
	}
}

func TestToGenResponse_WithToolUse(t *testing.T) {
	output := &bedrockruntime.ConverseOutput{
		Output: &brtypes.ConverseOutputMemberMessage{
			Value: brtypes.Message{
				Role: brtypes.ConversationRoleAssistant,
				Content: []brtypes.ContentBlock{
					&brtypes.ContentBlockMemberToolUse{
						Value: brtypes.ToolUseBlock{
							ToolUseId: aws.String("tool_1"),
							Name:      aws.String("get_weather"),
							Input:     document.NewLazyDocument(map[string]interface{}{"location": "Seattle"}),
						},
					},
				},
			},
		},
		StopReason: brtypes.StopReasonToolUse,
		Usage:      &brtypes.TokenUsage{},
		Metrics:    &brtypes.ConverseMetrics{},
	}

	genResp := toGenResponse(output)
	if len(genResp.Candidates) != 1 {
		t.Fatalf("Candidates length = %d, want 1", len(genResp.Candidates))
	}
	if genResp.Candidates[0].FinishReason != genai.FinishReasonToolCall {
		t.Errorf("FinishReason = %v, want tool_call", genResp.Candidates[0].FinishReason)
	}
	msg := genResp.Candidates[0].Message
	if len(msg.Parts) != 1 {
		t.Fatalf("Parts length = %d, want 1", len(msg.Parts))
	}
	if msg.Parts[0].FuncCall == nil {
		t.Fatal("expected FuncCall part")
	}
	if msg.Parts[0].FuncCall.Id != "tool_1" {
		t.Errorf("FuncCall.Id = %q, want %q", msg.Parts[0].FuncCall.Id, "tool_1")
	}
	if msg.Parts[0].FuncCall.FunctionName != "get_weather" {
		t.Errorf("FuncCall.FunctionName = %q, want %q", msg.Parts[0].FuncCall.FunctionName, "get_weather")
	}
}

// --- Stream event conversion tests ---

func TestStreamEventToGenResponse_TextDelta(t *testing.T) {
	idx := int32(0)
	event := &brtypes.ConverseStreamOutputMemberContentBlockDelta{
		Value: brtypes.ContentBlockDeltaEvent{
			ContentBlockIndex: &idx,
			Delta:             &brtypes.ContentBlockDeltaMemberText{Value: "Hello"},
		},
	}
	genResp := streamEventToGenResponse(event)
	if genResp == nil {
		t.Fatal("expected non-nil GenResponse")
	}
	if len(genResp.Candidates) != 1 {
		t.Fatalf("Candidates length = %d, want 1", len(genResp.Candidates))
	}
	if genResp.Candidates[0].FinishReason != genai.FinishReasonInProgress {
		t.Errorf("FinishReason = %v, want null (in progress)", genResp.Candidates[0].FinishReason)
	}
	if genResp.Candidates[0].Message.Parts[0].Text.Content != "Hello" {
		t.Errorf("text = %q, want %q", genResp.Candidates[0].Message.Parts[0].Text.Content, "Hello")
	}
}

func TestStreamEventToGenResponse_MessageStop(t *testing.T) {
	event := &brtypes.ConverseStreamOutputMemberMessageStop{
		Value: brtypes.MessageStopEvent{
			StopReason: brtypes.StopReasonEndTurn,
		},
	}
	genResp := streamEventToGenResponse(event)
	if genResp == nil {
		t.Fatal("expected non-nil GenResponse")
	}
	if genResp.Candidates[0].FinishReason != genai.FinishReasonEndTurn {
		t.Errorf("FinishReason = %v, want end_turn", genResp.Candidates[0].FinishReason)
	}
}

func TestStreamEventToGenResponse_Metadata(t *testing.T) {
	inputTokens := int32(100)
	outputTokens := int32(50)
	totalTokens := int32(150)
	latencyMs := int64(300)

	event := &brtypes.ConverseStreamOutputMemberMetadata{
		Value: brtypes.ConverseStreamMetadataEvent{
			Usage: &brtypes.TokenUsage{
				InputTokens:  &inputTokens,
				OutputTokens: &outputTokens,
				TotalTokens:  &totalTokens,
			},
			Metrics: &brtypes.ConverseStreamMetrics{
				LatencyMs: &latencyMs,
			},
		},
	}

	genResp := streamEventToGenResponse(event)
	if genResp == nil {
		t.Fatal("expected non-nil GenResponse")
	}
	if genResp.Meta.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", genResp.Meta.InputTokens)
	}
	if genResp.Meta.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", genResp.Meta.OutputTokens)
	}
	if genResp.Meta.TotalTime != 300 {
		t.Errorf("TotalTime = %d, want 300", genResp.Meta.TotalTime)
	}
}

func TestStreamEventToGenResponse_MessageStart(t *testing.T) {
	event := &brtypes.ConverseStreamOutputMemberMessageStart{
		Value: brtypes.MessageStartEvent{
			Role: brtypes.ConversationRoleAssistant,
		},
	}
	genResp := streamEventToGenResponse(event)
	if genResp != nil {
		t.Error("expected nil GenResponse for MessageStart event")
	}
}

func TestStreamEventToGenResponse_ContentBlockStart(t *testing.T) {
	event := &brtypes.ConverseStreamOutputMemberContentBlockStart{
		Value: brtypes.ContentBlockStartEvent{},
	}
	genResp := streamEventToGenResponse(event)
	if genResp != nil {
		t.Error("expected nil GenResponse for ContentBlockStart event")
	}
}

func TestStreamEventToGenResponse_ContentBlockStop(t *testing.T) {
	event := &brtypes.ConverseStreamOutputMemberContentBlockStop{
		Value: brtypes.ContentBlockStopEvent{},
	}
	genResp := streamEventToGenResponse(event)
	if genResp != nil {
		t.Error("expected nil GenResponse for ContentBlockStop event")
	}
}

// --- Build input tests ---

func TestBuildConverseInput(t *testing.T) {
	msg := genai.NewTextMessage(genai.RoleUser, "What is Go?")
	options := genai.NewOptionsBuilder().
		SetMaxTokens(1024).
		SetTemperature(0.3).
		Add(genai.OptionSystemInstructions, "Be concise.").
		Build()

	input, err := buildConverseInput("anthropic.claude-3-sonnet", msg, options)
	if err != nil {
		t.Fatalf("buildConverseInput error: %v", err)
	}
	if *input.ModelId != "anthropic.claude-3-sonnet" {
		t.Errorf("ModelId = %q, want %q", *input.ModelId, "anthropic.claude-3-sonnet")
	}
	if len(input.System) != 1 {
		t.Errorf("System length = %d, want 1", len(input.System))
	}
	if len(input.Messages) != 1 {
		t.Errorf("Messages length = %d, want 1", len(input.Messages))
	}
	if input.InferenceConfig == nil {
		t.Fatal("expected non-nil InferenceConfig")
	}
	if *input.InferenceConfig.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %d, want 1024", *input.InferenceConfig.MaxTokens)
	}
}

func TestBuildConverseInput_SystemMessage(t *testing.T) {
	msg := genai.NewTextMessage(genai.RoleSystem, "You are helpful.")
	input, err := buildConverseInput("amazon.titan-text-premier-v1:0", msg, nil)
	if err != nil {
		t.Fatalf("buildConverseInput error: %v", err)
	}
	// System message should be in System, not Messages
	if len(input.System) != 1 {
		t.Errorf("System length = %d, want 1", len(input.System))
	}
	if len(input.Messages) != 0 {
		t.Errorf("Messages length = %d, want 0", len(input.Messages))
	}
}

func TestBuildConverseStreamInput(t *testing.T) {
	msg := genai.NewTextMessage(genai.RoleUser, "Tell me a story.")
	input, err := buildConverseStreamInput("anthropic.claude-3-haiku", msg, nil)
	if err != nil {
		t.Fatalf("buildConverseStreamInput error: %v", err)
	}
	if *input.ModelId != "anthropic.claude-3-haiku" {
		t.Errorf("ModelId = %q, want %q", *input.ModelId, "anthropic.claude-3-haiku")
	}
	if len(input.Messages) != 1 {
		t.Errorf("Messages length = %d, want 1", len(input.Messages))
	}
}

// --- Generate with mock tests ---

func TestGenerate_Success(t *testing.T) {
	inputTokens := int32(10)
	outputTokens := int32(20)
	totalTokens := int32(30)
	latencyMs := int64(100)

	mock := &mockConverseAPI{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			return &bedrockruntime.ConverseOutput{
				Output: &brtypes.ConverseOutputMemberMessage{
					Value: brtypes.Message{
						Role: brtypes.ConversationRoleAssistant,
						Content: []brtypes.ContentBlock{
							&brtypes.ContentBlockMemberText{Value: "Go is a programming language."},
						},
					},
				},
				StopReason: brtypes.StopReasonEndTurn,
				Usage: &brtypes.TokenUsage{
					InputTokens:  &inputTokens,
					OutputTokens: &outputTokens,
					TotalTokens:  &totalTokens,
				},
				Metrics: &brtypes.ConverseMetrics{
					LatencyMs: &latencyMs,
				},
			}, nil
		},
	}

	provider := &BedrockProvider{
		client:      mock,
		models:      []string{"test-model"},
		description: ProviderDescription,
		version:     ProviderVersion,
	}

	msg := genai.NewTextMessage(genai.RoleUser, "What is Go?")
	resp, err := provider.Generate(context.Background(), "test-model", msg, nil)
	if err != nil {
		t.Fatalf("Generate error: %v", err)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("Candidates length = %d, want 1", len(resp.Candidates))
	}
	if resp.Candidates[0].Message.Parts[0].Text.Content != "Go is a programming language." {
		t.Errorf("text = %q, want %q", resp.Candidates[0].Message.Parts[0].Text.Content, "Go is a programming language.")
	}
	if resp.Meta.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", resp.Meta.InputTokens)
	}
	if resp.Candidates[0].FinishReason != genai.FinishReasonEndTurn {
		t.Errorf("FinishReason = %v, want end_turn", resp.Candidates[0].FinishReason)
	}
}

func TestGenerate_APIError(t *testing.T) {
	mock := &mockConverseAPI{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			return nil, fmt.Errorf("access denied")
		},
	}

	provider := &BedrockProvider{
		client:      mock,
		models:      []string{"test-model"},
		description: ProviderDescription,
		version:     ProviderVersion,
	}

	msg := genai.NewTextMessage(genai.RoleUser, "Hello")
	_, err := provider.Generate(context.Background(), "test-model", msg, nil)
	if err == nil {
		t.Fatal("expected error from Generate")
	}
	if !contains(err.Error(), "access denied") {
		t.Errorf("error = %q, expected to contain 'access denied'", err.Error())
	}
}

func TestGenerateStream_APIError(t *testing.T) {
	mock := &mockConverseAPI{
		converseStreamFunc: func(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
			return nil, fmt.Errorf("throttling exception")
		},
	}

	provider := &BedrockProvider{
		client:      mock,
		models:      []string{"test-model"},
		description: ProviderDescription,
		version:     ProviderVersion,
	}

	msg := genai.NewTextMessage(genai.RoleUser, "Hello")
	respChan, errChan := provider.GenerateStream(context.Background(), "test-model", msg, nil)
	// Drain response channel
	for range respChan {
	}
	err := <-errChan
	if err == nil {
		t.Fatal("expected error from GenerateStream")
	}
	if !contains(err.Error(), "throttling exception") {
		t.Errorf("error = %q, expected to contain 'throttling exception'", err.Error())
	}
}

// --- Bedrock message to genai message tests ---

func TestBedrockMessageToGenMessage_Text(t *testing.T) {
	brMsg := &brtypes.Message{
		Role: brtypes.ConversationRoleAssistant,
		Content: []brtypes.ContentBlock{
			&brtypes.ContentBlockMemberText{Value: "Hello!"},
		},
	}
	genMsg := bedrockMessageToGenMessage(brMsg)
	if genMsg.Role != genai.RoleAssistant {
		t.Errorf("Role = %v, want assistant", genMsg.Role)
	}
	if len(genMsg.Parts) != 1 {
		t.Fatalf("Parts length = %d, want 1", len(genMsg.Parts))
	}
	if genMsg.Parts[0].Text == nil || genMsg.Parts[0].Text.Content != "Hello!" {
		t.Error("expected text part with 'Hello!'")
	}
}

func TestBedrockMessageToGenMessage_ToolUse(t *testing.T) {
	brMsg := &brtypes.Message{
		Role: brtypes.ConversationRoleAssistant,
		Content: []brtypes.ContentBlock{
			&brtypes.ContentBlockMemberToolUse{
				Value: brtypes.ToolUseBlock{
					ToolUseId: aws.String("tool_abc"),
					Name:      aws.String("search"),
					Input:     document.NewLazyDocument(map[string]interface{}{"query": "golang"}),
				},
			},
		},
	}

	genMsg := bedrockMessageToGenMessage(brMsg)
	if len(genMsg.Parts) != 1 {
		t.Fatalf("Parts length = %d, want 1", len(genMsg.Parts))
	}
	fc := genMsg.Parts[0].FuncCall
	if fc == nil {
		t.Fatal("expected FuncCall part")
	}
	if fc.Id != "tool_abc" {
		t.Errorf("FuncCall.Id = %q, want %q", fc.Id, "tool_abc")
	}
	if fc.FunctionName != "search" {
		t.Errorf("FuncCall.FunctionName = %q, want %q", fc.FunctionName, "search")
	}
	if fc.Arguments["query"] != "golang" {
		t.Errorf("FuncCall.Arguments[query] = %v, want 'golang'", fc.Arguments["query"])
	}
}

func TestBedrockMessageToGenMessage_EmptyText(t *testing.T) {
	brMsg := &brtypes.Message{
		Role: brtypes.ConversationRoleAssistant,
		Content: []brtypes.ContentBlock{
			&brtypes.ContentBlockMemberText{Value: ""},
		},
	}
	genMsg := bedrockMessageToGenMessage(brMsg)
	// Empty text blocks are skipped
	if len(genMsg.Parts) != 0 {
		t.Errorf("Parts length = %d, want 0 (empty text skipped)", len(genMsg.Parts))
	}
}

// --- Provider interface compliance test ---

func TestProviderInterface(t *testing.T) {
	var _ genai.Provider = (*BedrockProvider)(nil)
}

// --- Helper ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
