package sns

import (
	"context"
	"net/url"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sns/types"
	"oss.nandlabs.io/golly/messaging"
	"oss.nandlabs.io/golly/uuid"
)

const (
	SchemeSns = "sns"
)

var snsSchemes = []string{SchemeSns}

type ProviderSNS struct {
	mutex sync.Mutex
}

func (snsp *ProviderSNS) Schemes() (schemes []string) {
	schemes = snsSchemes
	return
}

func (snsp *ProviderSNS) Setup() (err error) {
	return
}

func (snsp *ProviderSNS) NewMessage(scheme string, options ...messaging.Option) (msg messaging.Message, err error) {
	msg = NewSNSMessage()
	return
}

func (snsp *ProviderSNS) Send(url *url.URL, msg messaging.Message, options ...messaging.Option) (err error) {
	client, err := GetClient(url)
	if err != nil {
		return
	}

	_, err = client.Publish(context.Background(), &sns.PublishInput{
		TopicArn:         aws.String(""),
		Message:          aws.String(msg.ReadAsStr()),
		MessageStructure: aws.String("json"),
	})
	return
}

func (snsp *ProviderSNS) SendBatch(url *url.URL, msgs []messaging.Message, options ...messaging.Option) (err error) {
	client, err := GetClient(url)
	if err != nil {
		return
	}
	var publishBatchEntries []types.PublishBatchRequestEntry
	for _, msg := range msgs {
		itemId, err := uuid.V4()
		if err != nil {
			return err
		}
		input := types.PublishBatchRequestEntry{
			Id:      aws.String(itemId.String()),
			Message: aws.String(msg.ReadAsStr()),
		}
		publishBatchEntries = append(publishBatchEntries, input)
	}
	publishBatchInput := &sns.PublishBatchInput{
		PublishBatchRequestEntries: publishBatchEntries,
		TopicArn:                   aws.String(""),
	}
	output, err := client.PublishBatch(context.Background(), publishBatchInput)
	logger.Info(output.ResultMetadata)
	return
}

func (snsp *ProviderSNS) Receive(source *url.URL, options ...messaging.Option) (msg messaging.Message, err error) {
	return
}

func (snsp *ProviderSNS) ReceiveBatch(source *url.URL, options ...messaging.Option) (msgs []messaging.Message, err error) {
	return
}

func (snsp *ProviderSNS) AddListener(source *url.URL, listener func(msg messaging.Message), options ...messaging.Option) (err error) {
	return
}

func (snsp *ProviderSNS) Close() (err error) {
	// TODO should be used to close the listener
	return
}

func (snsp *ProviderSNS) Id() string {
	// TODO :: what needs to be provided here with ID
	return ""
}
