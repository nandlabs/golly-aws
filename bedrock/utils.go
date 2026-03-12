package bedrock

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"oss.nandlabs.io/golly/genai"
	"oss.nandlabs.io/golly/ioutils"
)

// buildConverseInput constructs a ConverseInput from genai types.
func buildConverseInput(model string, message *genai.Message, options *genai.Options) (*bedrockruntime.ConverseInput, error) {
	input := &bedrockruntime.ConverseInput{
		ModelId: aws.String(model),
	}

	// Extract system content from options and system-role message
	systemBlocks := extractSystemContent(message, options)
	if len(systemBlocks) > 0 {
		input.System = systemBlocks
	}

	// Convert the message (skip system-role messages as they are handled above)
	if message != nil && message.Role != genai.RoleSystem {
		brMsg, err := convertMessage(message)
		if err != nil {
			return nil, err
		}
		input.Messages = []brtypes.Message{brMsg}
	}

	// Build inference configuration from options
	if options != nil {
		if cfg := buildInferenceConfig(options); cfg != nil {
			input.InferenceConfig = cfg
		}
	}

	return input, nil
}

// buildConverseStreamInput constructs a ConverseStreamInput from genai types.
func buildConverseStreamInput(model string, message *genai.Message, options *genai.Options) (*bedrockruntime.ConverseStreamInput, error) {
	input := &bedrockruntime.ConverseStreamInput{
		ModelId: aws.String(model),
	}

	systemBlocks := extractSystemContent(message, options)
	if len(systemBlocks) > 0 {
		input.System = systemBlocks
	}

	if message != nil && message.Role != genai.RoleSystem {
		brMsg, err := convertMessage(message)
		if err != nil {
			return nil, err
		}
		input.Messages = []brtypes.Message{brMsg}
	}

	if options != nil {
		if cfg := buildInferenceConfig(options); cfg != nil {
			input.InferenceConfig = cfg
		}
	}

	return input, nil
}

// extractSystemContent extracts system instructions from genai types into Bedrock SystemContentBlocks.
func extractSystemContent(message *genai.Message, options *genai.Options) []brtypes.SystemContentBlock {
	var blocks []brtypes.SystemContentBlock

	// System instructions from options
	if options != nil {
		if sysInstr := options.GetSystemInstructions(); sysInstr != "" {
			blocks = append(blocks, &brtypes.SystemContentBlockMemberText{Value: sysInstr})
		}
	}

	// If the message itself is a system role, extract its text parts
	if message != nil && message.Role == genai.RoleSystem {
		for _, part := range message.Parts {
			if part.Text != nil && part.Text.Content != "" {
				blocks = append(blocks, &brtypes.SystemContentBlockMemberText{Value: part.Text.Content})
			}
		}
	}

	return blocks
}

// buildInferenceConfig constructs an InferenceConfiguration from genai Options.
func buildInferenceConfig(options *genai.Options) *brtypes.InferenceConfiguration {
	config := &brtypes.InferenceConfiguration{}
	hasConfig := false

	if options.Has(genai.OptionMaxTokens) {
		v := int32(options.GetMaxTokens(DefaultMaxTokens))
		config.MaxTokens = &v
		hasConfig = true
	}

	if options.Has(genai.OptionTemperature) {
		v := options.GetTemperature(0.7)
		config.Temperature = &v
		hasConfig = true
	}

	if options.Has(genai.OptionTopP) {
		v := options.GetTopP(0.9)
		config.TopP = &v
		hasConfig = true
	}

	if stopWords := options.GetStopWords(nil); len(stopWords) > 0 {
		config.StopSequences = stopWords
		hasConfig = true
	}

	if !hasConfig {
		return nil
	}
	return config
}

// convertMessage converts a genai.Message to a Bedrock Message.
func convertMessage(msg *genai.Message) (brtypes.Message, error) {
	brMsg := brtypes.Message{
		Role: convertRole(msg.Role),
	}

	for _, part := range msg.Parts {
		block, err := convertPart(&part)
		if err != nil {
			return brMsg, err
		}
		if block != nil {
			brMsg.Content = append(brMsg.Content, block)
		}
	}

	// Ensure at least one content block
	if len(brMsg.Content) == 0 {
		brMsg.Content = append(brMsg.Content, &brtypes.ContentBlockMemberText{Value: ""})
	}

	return brMsg, nil
}

// convertPart converts a genai.Part to a Bedrock ContentBlock.
func convertPart(part *genai.Part) (brtypes.ContentBlock, error) {
	switch {
	case part.Text != nil:
		return &brtypes.ContentBlockMemberText{Value: part.Text.Content}, nil

	case part.Bin != nil && ioutils.IsImageMime(part.MimeType):
		format, err := mimeToImageFormat(part.MimeType)
		if err != nil {
			return nil, err
		}
		return &brtypes.ContentBlockMemberImage{
			Value: brtypes.ImageBlock{
				Format: format,
				Source: &brtypes.ImageSourceMemberBytes{Value: part.Bin.Data},
			},
		}, nil

	case part.Bin != nil:
		// Non-image binary — try to treat as document with inferred format
		format, err := mimeToDocumentFormat(part.MimeType)
		if err != nil {
			// Fallback: encode as base64 text
			encoded := base64.StdEncoding.EncodeToString(part.Bin.Data)
			return &brtypes.ContentBlockMemberText{
				Value: fmt.Sprintf("[binary data %s base64]: %s", part.MimeType, encoded),
			}, nil
		}
		name := part.Name
		if name == "" {
			name = "document"
		}
		return &brtypes.ContentBlockMemberDocument{
			Value: brtypes.DocumentBlock{
				Format: format,
				Name:   aws.String(name),
				Source: &brtypes.DocumentSourceMemberBytes{Value: part.Bin.Data},
			},
		}, nil

	case part.File != nil && ioutils.IsImageMime(part.MimeType):
		// Bedrock doesn't support image URLs directly — include as text reference
		return &brtypes.ContentBlockMemberText{
			Value: fmt.Sprintf("[Image URL: %s]", part.File.URI),
		}, nil

	case part.FuncCall != nil:
		return &brtypes.ContentBlockMemberToolUse{
			Value: brtypes.ToolUseBlock{
				ToolUseId: aws.String(part.FuncCall.Id),
				Name:      aws.String(part.FuncCall.FunctionName),
				Input:     document.NewLazyDocument(part.FuncCall.Arguments),
			},
		}, nil

	case part.FuncResponse != nil:
		var content []brtypes.ToolResultContentBlock
		if part.FuncResponse.Text != nil {
			content = append(content, &brtypes.ToolResultContentBlockMemberText{
				Value: *part.FuncResponse.Text,
			})
		} else if part.FuncResponse.Data != nil {
			content = append(content, &brtypes.ToolResultContentBlockMemberText{
				Value: string(part.FuncResponse.Data),
			})
		}
		return &brtypes.ContentBlockMemberToolResult{
			Value: brtypes.ToolResultBlock{
				ToolUseId: aws.String(part.Name),
				Content:   content,
			},
		}, nil

	default:
		return nil, nil
	}
}

// convertRole maps genai.Role to Bedrock ConversationRole.
func convertRole(role genai.Role) brtypes.ConversationRole {
	switch role {
	case genai.RoleUser:
		return brtypes.ConversationRoleUser
	case genai.RoleAssistant:
		return brtypes.ConversationRoleAssistant
	default:
		return brtypes.ConversationRoleUser
	}
}

// convertFromBedrockRole maps Bedrock ConversationRole to genai.Role.
func convertFromBedrockRole(role brtypes.ConversationRole) genai.Role {
	switch role {
	case brtypes.ConversationRoleUser:
		return genai.RoleUser
	case brtypes.ConversationRoleAssistant:
		return genai.RoleAssistant
	default:
		return genai.RoleAssistant
	}
}

// mapStopReason maps Bedrock StopReason to genai.FinishReason.
func mapStopReason(reason brtypes.StopReason) genai.FinishReason {
	switch reason {
	case brtypes.StopReasonEndTurn:
		return genai.FinishReasonEndTurn
	case brtypes.StopReasonMaxTokens:
		return genai.FinishReasonLength
	case brtypes.StopReasonStopSequence:
		return genai.FinishReasonStop
	case brtypes.StopReasonToolUse:
		return genai.FinishReasonToolCall
	case brtypes.StopReasonContentFiltered:
		return genai.FinishReasonContentFilter
	default:
		return genai.FinishReasonUnknown
	}
}

// toGenResponse converts a Bedrock ConverseOutput to genai.GenResponse.
func toGenResponse(output *bedrockruntime.ConverseOutput) *genai.GenResponse {
	genResp := &genai.GenResponse{
		Meta: genai.ResponseMeta{},
	}

	// Map token usage
	if output.Usage != nil {
		if output.Usage.InputTokens != nil {
			genResp.Meta.InputTokens = int(*output.Usage.InputTokens)
		}
		if output.Usage.OutputTokens != nil {
			genResp.Meta.OutputTokens = int(*output.Usage.OutputTokens)
		}
		if output.Usage.TotalTokens != nil {
			genResp.Meta.TotalTokens = int(*output.Usage.TotalTokens)
		}
		if output.Usage.CacheReadInputTokens != nil {
			genResp.Meta.CachedTokens = int(*output.Usage.CacheReadInputTokens)
		}
	}

	// Map latency
	if output.Metrics != nil && output.Metrics.LatencyMs != nil {
		genResp.Meta.TotalTime = *output.Metrics.LatencyMs
	}

	// Map the response message
	if msgOutput, ok := output.Output.(*brtypes.ConverseOutputMemberMessage); ok {
		msg := bedrockMessageToGenMessage(&msgOutput.Value)
		genResp.Candidates = []genai.Candidate{
			{
				Index:        0,
				Message:      msg,
				FinishReason: mapStopReason(output.StopReason),
			},
		}
	}

	return genResp
}

// bedrockMessageToGenMessage converts a Bedrock Message to a genai.Message.
func bedrockMessageToGenMessage(msg *brtypes.Message) *genai.Message {
	genMsg := &genai.Message{
		Role:  convertFromBedrockRole(msg.Role),
		Parts: []genai.Part{},
	}

	for _, block := range msg.Content {
		switch b := block.(type) {
		case *brtypes.ContentBlockMemberText:
			if b.Value != "" {
				genMsg.Parts = append(genMsg.Parts, genai.Part{
					Name:     "text",
					MimeType: ioutils.MimeTextPlain,
					Text:     &genai.TextPart{Content: b.Value},
				})
			}

		case *brtypes.ContentBlockMemberToolUse:
			args := make(map[string]interface{})
			if b.Value.Input != nil {
				// Use UnmarshalSmithyDocument to decode the document.Interface to a map
				if err := b.Value.Input.UnmarshalSmithyDocument(&args); err != nil {
					logger.WarnF("failed to unmarshal tool use input: %v", err)
				}
			}
			toolName := ""
			if b.Value.Name != nil {
				toolName = *b.Value.Name
			}
			toolId := ""
			if b.Value.ToolUseId != nil {
				toolId = *b.Value.ToolUseId
			}
			genMsg.Parts = append(genMsg.Parts, genai.Part{
				Name: toolName,
				FuncCall: &genai.FuncCallPart{
					Id:           toolId,
					FunctionName: toolName,
					Arguments:    args,
				},
			})

		case *brtypes.ContentBlockMemberImage:
			// Extract image bytes from response if available
			if src, ok := b.Value.Source.(*brtypes.ImageSourceMemberBytes); ok {
				genMsg.Parts = append(genMsg.Parts, genai.Part{
					Name:     "image",
					MimeType: imageFormatToMime(b.Value.Format),
					Bin:      &genai.BinPart{Data: src.Value},
				})
			}
		}
	}

	return genMsg
}

// streamEventToGenResponse converts a Bedrock stream event to a genai.GenResponse.
// Returns nil for events that don't produce a response (e.g., content block start/stop).
func streamEventToGenResponse(event brtypes.ConverseStreamOutput) *genai.GenResponse {
	switch e := event.(type) {
	case *brtypes.ConverseStreamOutputMemberContentBlockDelta:
		if delta, ok := e.Value.Delta.(*brtypes.ContentBlockDeltaMemberText); ok {
			idx := 0
			if e.Value.ContentBlockIndex != nil {
				idx = int(*e.Value.ContentBlockIndex)
			}
			return &genai.GenResponse{
				Candidates: []genai.Candidate{
					{
						Index:        idx,
						Message:      genai.NewTextMessage(genai.RoleAssistant, delta.Value),
						FinishReason: genai.FinishReasonInProgress,
					},
				},
			}
		}
		return nil

	case *brtypes.ConverseStreamOutputMemberMessageStop:
		return &genai.GenResponse{
			Candidates: []genai.Candidate{
				{
					Index:        0,
					Message:      genai.NewTextMessage(genai.RoleAssistant, ""),
					FinishReason: mapStopReason(e.Value.StopReason),
				},
			},
		}

	case *brtypes.ConverseStreamOutputMemberMetadata:
		genResp := &genai.GenResponse{}
		if e.Value.Usage != nil {
			if e.Value.Usage.InputTokens != nil {
				genResp.Meta.InputTokens = int(*e.Value.Usage.InputTokens)
			}
			if e.Value.Usage.OutputTokens != nil {
				genResp.Meta.OutputTokens = int(*e.Value.Usage.OutputTokens)
			}
			if e.Value.Usage.TotalTokens != nil {
				genResp.Meta.TotalTokens = int(*e.Value.Usage.TotalTokens)
			}
			if e.Value.Usage.CacheReadInputTokens != nil {
				genResp.Meta.CachedTokens = int(*e.Value.Usage.CacheReadInputTokens)
			}
		}
		if e.Value.Metrics != nil && e.Value.Metrics.LatencyMs != nil {
			genResp.Meta.TotalTime = *e.Value.Metrics.LatencyMs
		}
		return genResp

	case *brtypes.ConverseStreamOutputMemberMessageStart:
		// Provides the role — not much to emit as a GenResponse
		return nil

	case *brtypes.ConverseStreamOutputMemberContentBlockStart:
		// Content block start — nothing to emit
		return nil

	case *brtypes.ConverseStreamOutputMemberContentBlockStop:
		// Content block stop — nothing to emit
		return nil

	default:
		return nil
	}
}

// mimeToImageFormat converts a MIME type to a Bedrock ImageFormat.
func mimeToImageFormat(mime string) (brtypes.ImageFormat, error) {
	switch strings.ToLower(mime) {
	case "image/png":
		return brtypes.ImageFormatPng, nil
	case "image/jpeg", "image/jpg":
		return brtypes.ImageFormatJpeg, nil
	case "image/gif":
		return brtypes.ImageFormatGif, nil
	case "image/webp":
		return brtypes.ImageFormatWebp, nil
	default:
		return "", fmt.Errorf("unsupported image format: %s", mime)
	}
}

// imageFormatToMime converts a Bedrock ImageFormat to a MIME type.
func imageFormatToMime(format brtypes.ImageFormat) string {
	switch format {
	case brtypes.ImageFormatPng:
		return "image/png"
	case brtypes.ImageFormatJpeg:
		return "image/jpeg"
	case brtypes.ImageFormatGif:
		return "image/gif"
	case brtypes.ImageFormatWebp:
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// mimeToDocumentFormat converts a MIME type to a Bedrock DocumentFormat.
func mimeToDocumentFormat(mime string) (brtypes.DocumentFormat, error) {
	switch strings.ToLower(mime) {
	case "application/pdf":
		return brtypes.DocumentFormatPdf, nil
	case "text/csv":
		return brtypes.DocumentFormatCsv, nil
	case "application/msword":
		return brtypes.DocumentFormatDoc, nil
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return brtypes.DocumentFormatDocx, nil
	case "application/vnd.ms-excel":
		return brtypes.DocumentFormatXls, nil
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return brtypes.DocumentFormatXlsx, nil
	case "text/html":
		return brtypes.DocumentFormatHtml, nil
	case "text/plain":
		return brtypes.DocumentFormatTxt, nil
	case "text/markdown":
		return brtypes.DocumentFormatMd, nil
	default:
		return "", fmt.Errorf("unsupported document format: %s", mime)
	}
}
