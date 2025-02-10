package sns

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

func GetClient(url *url.URL) (client *sns.Client, err error) {
	err = validateMessaginUrl(url.String())
	if err != nil {
		return
	}
	client, err = CreateSNSClient(url)
	if err != nil {
		return
	}
	return
}

func CreateSNSClient(url *url.URL) (client *sns.Client, err error) {
	var awsSession *aws.Config
	awsSession, err = GetSession(url.Host)
	if err != nil {
		return
	}
	client = sns.NewFromConfig(*awsSession)
	return
}

func validateMessaginUrl(input string) (err error) {
	parsedURL, err := url.Parse(input)
	if err != nil {
		err = errors.New("url parsing failed")
		return // URL parsing failed
	}

	// Check if the scheme is "https"
	if parsedURL.Scheme != "https" {
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
