package sqs

import (
	"context"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"oss.nandlabs.io/golly-aws/awscfg"
)

const (
	// SQSScheme is the URL scheme for SQS.
	SQSScheme = "sqs"
	// SQSProviderID is the provider identifier.
	SQSProviderID = "sqs-provider"
)

// getSQSClient creates an SQS client using the awscfg config resolved for the given URL.
func getSQSClient(u *url.URL) (*sqs.Client, error) {
	cfg := awscfg.GetConfig(u, SQSScheme)
	if cfg == nil {
		// Fallback: load default AWS config
		awsCfg, err := (&awscfg.Config{}).LoadAWSConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("sqs: failed to load default AWS config: %w", err)
		}
		return sqs.NewFromConfig(awsCfg), nil
	}

	awsCfg, err := cfg.LoadAWSConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("sqs: failed to load AWS config: %w", err)
	}

	var sqsOpts []func(*sqs.Options)
	if cfg.Endpoint != "" {
		sqsOpts = append(sqsOpts, func(o *sqs.Options) {
			o.BaseEndpoint = &cfg.Endpoint
		})
	}

	return sqs.NewFromConfig(awsCfg, sqsOpts...), nil
}

// resolveQueueURL resolves the SQS queue URL from the messaging URL.
// URL format: sqs://queue-name  or  sqs://queue-name/account-id
// If a custom endpoint is configured (e.g., LocalStack), it constructs the URL directly.
// Otherwise, it calls GetQueueUrl API.
func resolveQueueURL(client *sqs.Client, u *url.URL) (string, error) {
	queueName := u.Host
	if queueName == "" {
		return "", fmt.Errorf("sqs: queue name (URL host) is required")
	}

	// Check if we have a custom endpoint (LocalStack/ElasticMQ)
	cfg := awscfg.GetConfig(u, SQSScheme)
	if cfg != nil && cfg.Endpoint != "" {
		// For local endpoints, construct URL directly
		accountID := "000000000000"
		if u.Path != "" && len(u.Path) > 1 {
			accountID = u.Path[1:] // strip leading /
		}
		return fmt.Sprintf("%s/%s/%s", cfg.Endpoint, accountID, queueName), nil
	}

	// Use GetQueueUrl API for real AWS
	input := &sqs.GetQueueUrlInput{
		QueueName: &queueName,
	}
	// If path contains account ID, use it
	if u.Path != "" && len(u.Path) > 1 {
		accountID := u.Path[1:]
		input.QueueOwnerAWSAccountId = &accountID
	}

	output, err := client.GetQueueUrl(context.Background(), input)
	if err != nil {
		return "", fmt.Errorf("sqs: failed to get queue URL for %q: %w", queueName, err)
	}
	return *output.QueueUrl, nil
}
