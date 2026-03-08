package bedrock

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"oss.nandlabs.io/golly-aws/awscfg"
	"oss.nandlabs.io/golly/genai"
)

const (
	// ProviderName is the name of the Bedrock provider.
	ProviderName = "bedrock"
	// ProviderVersion is the version of the Bedrock provider.
	ProviderVersion = "1.0.0"
	// ProviderDescription is the description of the Bedrock provider.
	ProviderDescription = "AWS Bedrock provider for foundation model inference via the Converse API"
	// DefaultMaxTokens is the default max tokens if not specified in options.
	DefaultMaxTokens = 4096
)

// BedrockProvider implements the genai.Provider interface for AWS Bedrock
// using the Converse and ConverseStream APIs.
type BedrockProvider struct {
	client      converseAPI
	models      []string
	description string
	version     string
}

// ProviderConfig contains configuration for the Bedrock provider.
type ProviderConfig struct {
	// CfgName is the name of the awscfg.Config registered with awscfg.Manager.
	// If empty and Config is nil, the default AWS config is loaded.
	CfgName string
	// Config is an explicit awscfg.Config. Overrides CfgName when set.
	Config *awscfg.Config
	// Models is the list of Bedrock model IDs supported by this provider instance.
	Models []string
	// Description is a custom description for the provider.
	Description string
	// Version is a custom version for the provider.
	Version string
}

// NewBedrockProvider creates a new BedrockProvider with the given configuration.
// It resolves the AWS Bedrock runtime client using the provided config or awscfg manager.
func NewBedrockProvider(config *ProviderConfig) (*BedrockProvider, error) {
	if config == nil {
		config = &ProviderConfig{}
	}

	var client *bedrockruntime.Client
	var err error

	if config.Config != nil {
		awsCfg, loadErr := config.Config.LoadAWSConfig(context.Background())
		if loadErr != nil {
			return nil, fmt.Errorf("failed to load AWS config: %w", loadErr)
		}
		var opts []func(*bedrockruntime.Options)
		if config.Config.Endpoint != "" {
			ep := config.Config.Endpoint
			opts = append(opts, func(o *bedrockruntime.Options) {
				o.BaseEndpoint = &ep
			})
		}
		client = bedrockruntime.NewFromConfig(awsCfg, opts...)
	} else {
		client, err = getBedrockClient(config.CfgName)
		if err != nil {
			return nil, err
		}
	}

	desc := config.Description
	if desc == "" {
		desc = ProviderDescription
	}
	ver := config.Version
	if ver == "" {
		ver = ProviderVersion
	}

	return &BedrockProvider{
		client:      client,
		models:      config.Models,
		description: desc,
		version:     ver,
	}, nil
}

// Name returns the name of the provider.
func (p *BedrockProvider) Name() string { return ProviderName }

// Description returns a brief description of the provider.
func (p *BedrockProvider) Description() string { return p.description }

// Version returns the version of the provider.
func (p *BedrockProvider) Version() string { return p.version }

// Models returns the list of model IDs supported by this provider instance.
func (p *BedrockProvider) Models() []string { return p.models }

// Close releases provider resources. The Bedrock client does not hold persistent
// connections, so this is a no-op.
func (p *BedrockProvider) Close() error { return nil }

// Generate performs a synchronous inference call using the Bedrock Converse API.
func (p *BedrockProvider) Generate(ctx context.Context, model string, message *genai.Message, options *genai.Options) (*genai.GenResponse, error) {
	input, err := buildConverseInput(model, message, options)
	if err != nil {
		return nil, fmt.Errorf("failed to build converse input: %w", err)
	}

	output, err := p.client.Converse(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("bedrock Converse API call failed: %w", err)
	}

	return toGenResponse(output), nil
}

// GenerateStream performs a streaming inference call using the Bedrock ConverseStream API.
func (p *BedrockProvider) GenerateStream(ctx context.Context, model string, message *genai.Message, options *genai.Options) (<-chan *genai.GenResponse, <-chan error) {
	responseChan := make(chan *genai.GenResponse, 10)
	errorChan := make(chan error, 1)

	go func() {
		defer close(responseChan)
		defer close(errorChan)

		input, err := buildConverseStreamInput(model, message, options)
		if err != nil {
			errorChan <- fmt.Errorf("failed to build converse stream input: %w", err)
			return
		}

		output, err := p.client.ConverseStream(ctx, input)
		if err != nil {
			errorChan <- fmt.Errorf("bedrock ConverseStream API call failed: %w", err)
			return
		}

		stream := output.GetStream()
		if stream == nil {
			errorChan <- fmt.Errorf("bedrock ConverseStream returned nil stream")
			return
		}
		defer func() {
			if closeErr := stream.Close(); closeErr != nil {
				logger.ErrorF("failed to close bedrock stream: %v", closeErr)
			}
		}()

		for event := range stream.Events() {
			select {
			case <-ctx.Done():
				errorChan <- ctx.Err()
				return
			default:
			}

			genResp := streamEventToGenResponse(event)
			if genResp != nil {
				responseChan <- genResp
			}
		}

		if err := stream.Err(); err != nil {
			errorChan <- fmt.Errorf("bedrock streaming error: %w", err)
		}
	}()

	return responseChan, errorChan
}
