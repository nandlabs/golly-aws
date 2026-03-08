package bedrock

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"oss.nandlabs.io/golly-aws/awscfg"
	"oss.nandlabs.io/golly/l3"
)

var logger = l3.Get()

// converseAPI abstracts the Bedrock runtime client methods used by the provider.
// This interface enables testing with mock implementations.
type converseAPI interface {
	Converse(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
	ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

// getBedrockClient creates a Bedrock runtime client from the named awscfg.Config.
// If cfgName is empty and no config is registered, the default AWS config is loaded.
func getBedrockClient(cfgName string) (*bedrockruntime.Client, error) {
	cfg := awscfg.Manager.Get(cfgName)
	if cfg == nil {
		// Fallback: load default AWS config without awscfg registration
		awsCfg, err := (&awscfg.Config{}).LoadAWSConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to load default AWS config: %w", err)
		}
		return bedrockruntime.NewFromConfig(awsCfg), nil
	}

	awsCfg, err := cfg.LoadAWSConfig(context.Background())
	if err != nil {
		return nil, err
	}

	var opts []func(*bedrockruntime.Options)
	if cfg.Endpoint != "" {
		ep := cfg.Endpoint
		opts = append(opts, func(o *bedrockruntime.Options) {
			o.BaseEndpoint = &ep
		})
	}

	return bedrockruntime.NewFromConfig(awsCfg, opts...), nil
}
