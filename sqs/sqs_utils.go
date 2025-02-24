package sqs

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"oss.nandlabs.io/golly-aws/awssvc"
	"oss.nandlabs.io/golly/textutils"
)

func GetClient(url *url.URL) (client *sqs.Client, err error) {
	err = validateMessagingUrl(url.String())
	if err != nil {
		return
	}
	client, err = CreateSQSClient(url)
	if err != nil {
		return
	}
	return
}

func CreateSQSClient(url *url.URL) (client *sqs.Client, err error) {
	awsConfig := awssvc.Manager.Get(awssvc.ExtractKey(url))
	if awsConfig.Region == textutils.EmptyStr {
		awsConfig = awssvc.Manager.Get("sqs")
		if awsConfig.Region == textutils.EmptyStr {
			awsConfig, err = config.LoadDefaultConfig(context.Background())
			if err != nil {
				return
			}
		}
	}
	if err != nil {
		return
	}
	client = sqs.NewFromConfig(awsConfig)
	return
}

func validateMessagingUrl(input string) (err error) {
	parsedURL, err := url.Parse(input)
	if err != nil {
		err = errors.New("url parsing failed")
		return // URL parsing failed
	}

	// Check if the scheme is "https"
	if parsedURL.Scheme != "sqs" {
		err = errors.New("invalid url scheme")
		return
	}

	// Define a regular expression to match the AWS SQS host pattern with a wildcard in the domain
	awsSQSHostPattern := `^sqs\.[^.]+\.amazonaws\.com$`
	match, _ := regexp.MatchString(awsSQSHostPattern, parsedURL.Host)
	if !match {
		err = errors.New("invalid AWS SQS host format")
		return
	}

	// Check if the path is not empty and starts with "/"
	if parsedURL.Path == "" || !strings.HasPrefix(parsedURL.Path, "/") {
		err = errors.New("invalid URL path")
		return
	}

	// Additional checks can be added here if needed, such as validating the AWS account ID and queue name.
	return
}
