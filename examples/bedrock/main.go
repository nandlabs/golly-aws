package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"oss.nandlabs.io/golly-aws/awscfg"
	"oss.nandlabs.io/golly-aws/bedrock"
	"oss.nandlabs.io/golly/genai"
)

func main() {
	region := envOrDefault("AWS_REGION", "us-east-1")
	model := envOrDefault("BEDROCK_MODEL", "anthropic.claude-3-haiku-20240307-v1:0")
	endpoint := os.Getenv("BEDROCK_ENDPOINT")

	fmt.Println("=== golly Bedrock Example ===")
	fmt.Printf("Region: %s | Model: %s\n\n", region, model)

	// Step 1: Configure AWS credentials via awscfg
	step("1. Configure AWS credentials")
	cfg := awscfg.NewConfig(region)
	if endpoint != "" {
		cfg.SetEndpoint(endpoint)
		cfg.SetStaticCredentials("test", "test", "")
		fmt.Printf("   Using custom endpoint: %s\n", endpoint)
	}
	fmt.Println("   Config created")

	// Step 2: Create Bedrock provider
	step("2. Create Bedrock provider")
	provider, err := bedrock.NewBedrockProvider(&bedrock.ProviderConfig{
		Config: cfg,
		Models: []string{model},
	})
	check(err, "NewBedrockProvider")
	fmt.Printf("   Provider: %s v%s\n", provider.Name(), provider.Version())
	fmt.Printf("   Description: %s\n", provider.Description())
	fmt.Printf("   Models: %v\n", provider.Models())

	// Step 3: Simple text generation
	step("3. Simple text generation")
	message := genai.NewTextMessage(genai.RoleUser, "What is the capital of France? Answer in one sentence.")
	options := genai.NewOptionsBuilder().
		SetMaxTokens(256).
		SetTemperature(0.3).
		Build()

	resp, err := provider.Generate(context.Background(), model, message, options)
	check(err, "Generate")
	printResponse("Simple", resp)

	// Step 4: Generation with system instructions
	step("4. Generation with system instructions")
	message2 := genai.NewTextMessage(genai.RoleUser, "Explain quantum entanglement.")
	options2 := genai.NewOptionsBuilder().
		SetMaxTokens(512).
		SetTemperature(0.5).
		Add(genai.OptionSystemInstructions, "You are a science teacher. Explain concepts simply, suitable for a 10-year-old. Use analogies.").
		Build()

	resp2, err := provider.Generate(context.Background(), model, message2, options2)
	check(err, "Generate with system instructions")
	printResponse("System Instructions", resp2)

	// Step 5: JSON output generation
	step("5. JSON output generation")
	message3 := genai.NewTextMessage(genai.RoleUser,
		`Return a JSON object with the following structure:
{"name": "<country>", "capital": "<capital>", "population": <number>}
for the country Japan. Only return the JSON, no other text.`)
	options3 := genai.NewOptionsBuilder().
		SetMaxTokens(256).
		SetTemperature(0.0).
		Build()

	resp3, err := provider.Generate(context.Background(), model, message3, options3)
	check(err, "Generate JSON")
	printResponse("JSON Output", resp3)

	// Step 6: Streaming generation
	step("6. Streaming generation")
	message4 := genai.NewTextMessage(genai.RoleUser, "Write a haiku about cloud computing.")
	options4 := genai.NewOptionsBuilder().
		SetMaxTokens(128).
		SetTemperature(0.7).
		Build()

	responseChan, errorChan := provider.GenerateStream(context.Background(), model, message4, options4)
	fmt.Print("   Stream: ")
	streamTokens := 0
	for resp := range responseChan {
		for _, c := range resp.Candidates {
			if c.Message != nil {
				for _, p := range c.Message.Parts {
					if p.Text != nil {
						fmt.Print(p.Text.Text)
						streamTokens++
					}
				}
			}
			if c.FinishReason != genai.FinishReasonInProgress && c.FinishReason != "" {
				fmt.Printf("\n   Finish reason: %s\n", c.FinishReason)
			}
		}
		if resp.Meta.InputTokens > 0 || resp.Meta.OutputTokens > 0 {
			fmt.Printf("   Tokens: input=%d, output=%d, total=%d\n",
				resp.Meta.InputTokens, resp.Meta.OutputTokens, resp.Meta.TotalTokens)
		}
	}
	if err := <-errorChan; err != nil {
		fmt.Printf("   Stream error: %v\n", err)
	}
	fmt.Println()

	// Step 7: Generation with stop sequences
	step("7. Generation with stop sequences")
	message5 := genai.NewTextMessage(genai.RoleUser, "Count from 1 to 20, one per line.")
	options5 := genai.NewOptionsBuilder().
		SetMaxTokens(512).
		SetTemperature(0.0).
		SetStopWords("10").
		Build()

	resp5, err := provider.Generate(context.Background(), model, message5, options5)
	check(err, "Generate with stop sequences")
	printResponse("Stop Sequences", resp5)

	// Step 8: TopP sampling
	step("8. TopP sampling")
	message6 := genai.NewTextMessage(genai.RoleUser, "Write a creative one-sentence story about a robot.")
	options6 := genai.NewOptionsBuilder().
		SetMaxTokens(256).
		SetTemperature(0.9).
		SetTopP(0.95).
		Build()

	resp6, err := provider.Generate(context.Background(), model, message6, options6)
	check(err, "Generate with TopP")
	printResponse("TopP", resp6)

	// Step 9: Multipart message
	step("9. Multipart message (multiple text parts)")
	msg := &genai.Message{
		Role: genai.RoleUser,
		Parts: []genai.Part{
			{
				Name:     "context",
				MimeType: "text/plain",
				Text:     &genai.TextPart{Text: "The Eiffel Tower was built for the 1889 World's Fair in Paris."},
			},
			{
				Name:     "question",
				MimeType: "text/plain",
				Text:     &genai.TextPart{Text: "When was the Eiffel Tower built and for what event?"},
			},
		},
	}
	options7 := genai.NewOptionsBuilder().
		SetMaxTokens(256).
		SetTemperature(0.0).
		Build()

	resp7, err := provider.Generate(context.Background(), model, msg, options7)
	check(err, "Generate multipart")
	printResponse("Multipart", resp7)

	// Step 10: Close provider
	step("10. Close provider")
	err = provider.Close()
	check(err, "Close")
	fmt.Println("   Provider closed")

	fmt.Println("\n=== Example Complete ===")
}

// printResponse prints a GenResponse with candidate text and metadata.
func printResponse(label string, resp *genai.GenResponse) {
	if resp == nil {
		fmt.Printf("   [%s] No response\n", label)
		return
	}
	for _, c := range resp.Candidates {
		if c.Message != nil {
			for _, p := range c.Message.Parts {
				if p.Text != nil {
					text := p.Text.Text
					if len(text) > 300 {
						text = text[:300] + "..."
					}
					fmt.Printf("   Response: %s\n", text)
				}
			}
		}
		fmt.Printf("   Finish: %s\n", c.FinishReason)
	}
	fmt.Printf("   Tokens: input=%d, output=%d, total=%d\n",
		resp.Meta.InputTokens, resp.Meta.OutputTokens, resp.Meta.TotalTokens)
	if resp.Meta.CachedTokens > 0 {
		fmt.Printf("   Cached: %d\n", resp.Meta.CachedTokens)
	}
	if resp.Meta.TotalTime > 0 {
		fmt.Printf("   Latency: %dms\n", resp.Meta.TotalTime)
	}
}

func step(name string) {
	fmt.Printf("\n-- %s --\n", name)
}

func check(err error, action string) {
	if err != nil {
		log.Fatalf("FATAL: %s: %v", action, err)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
