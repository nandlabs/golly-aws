package sns

import (
	"context"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"oss.nandlabs.io/golly-aws/awscfg"
)

const (
	// SNSScheme is the URL scheme for SNS.
	SNSScheme = "sns"
	// SNSProviderID is the provider identifier.
	SNSProviderID = "sns-provider"
)

// getSNSClient creates an SNS client using the awscfg config resolved for the given URL.
func getSNSClient(u *url.URL) (*sns.Client, error) {
	cfg := awscfg.GetConfig(u, SNSScheme)
	if cfg == nil {
		// Fallback: load default AWS config
		awsCfg, err := (&awscfg.Config{}).LoadAWSConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("sns: failed to load default AWS config: %w", err)
		}
		return sns.NewFromConfig(awsCfg), nil
	}

	awsCfg, err := cfg.LoadAWSConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("sns: failed to load AWS config: %w", err)
	}

	var snsOpts []func(*sns.Options)
	if cfg.Endpoint != "" {
		snsOpts = append(snsOpts, func(o *sns.Options) {
			o.BaseEndpoint = &cfg.Endpoint
		})
	}

	return sns.NewFromConfig(awsCfg, snsOpts...), nil
}

// resolveTopicARN extracts the SNS topic ARN from the messaging URL.
//
// URL formats:
//
//	sns://topic-name                              → requires lookup via CreateTopic (idempotent)
//	sns:///arn:aws:sns:region:account-id:topic    → ARN in path directly
//
// When a custom endpoint is set, the topic name is used to call CreateTopic
// (which is idempotent and returns the ARN for existing topics).
// For real AWS, if the host looks like a plain topic name, CreateTopic is called.
// If the path starts with "arn:", it is treated as a literal ARN.
func resolveTopicARN(client *sns.Client, u *url.URL) (string, error) {
	// Check for ARN in path: sns:///arn:aws:sns:...
	if u.Host == "" && u.Path != "" {
		path := u.Path
		if len(path) > 0 && path[0] == '/' {
			path = path[1:]
		}
		if len(path) > 4 && path[:4] == "arn:" {
			return path, nil
		}
		return "", fmt.Errorf("sns: topic name or ARN is required")
	}

	topicName := u.Host
	if topicName == "" {
		return "", fmt.Errorf("sns: topic name (URL host) is required")
	}

	// Check if host is already a full ARN (shouldn't happen with URL parsing, but be safe)
	if len(topicName) > 4 && topicName[:4] == "arn:" {
		return topicName, nil
	}

	// Use CreateTopic to resolve name → ARN (idempotent for existing topics)
	output, err := client.CreateTopic(context.Background(), &sns.CreateTopicInput{
		Name: &topicName,
	})
	if err != nil {
		return "", fmt.Errorf("sns: failed to resolve topic ARN for %q: %w", topicName, err)
	}
	return *output.TopicArn, nil
}

func strPtr(s string) *string {
	return &s
}
