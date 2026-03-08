package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"oss.nandlabs.io/golly-aws/awscfg"
	snsprovider "oss.nandlabs.io/golly-aws/sns"
	"oss.nandlabs.io/golly/messaging"
)

func main() {
	region := envOrDefault("AWS_REGION", "us-east-1")
	topicName := envOrDefault("SNS_TOPIC", "golly-sns-example")
	endpoint := os.Getenv("SNS_ENDPOINT")

	cfg := awscfg.NewConfig(region)
	if endpoint != "" {
		cfg.SetEndpoint(endpoint)
		cfg.SetStaticCredentials("test", "test", "")
		fmt.Printf("Using custom endpoint: %s\n", endpoint)
	}
	awscfg.Manager.Register("sns", cfg)

	fmt.Println("=== golly SNS Example ===")
	fmt.Printf("Topic: %s | Region: %s\n\n", topicName, region)

	// Step 1: Create an SNS topic
	step("1. Create SNS topic")
	topicARN := createTopic(cfg, topicName)
	fmt.Printf("   Topic ARN: %s\n", topicARN)

	mgr := messaging.GetManager()
	u, _ := url.Parse(fmt.Sprintf("sns://%s", topicName))

	// Step 2: Send a single message
	step("2. Send a single message")
	msg, err := mgr.NewMessage("sns")
	check(err, "NewMessage")
	_, err = msg.SetBodyStr("Hello from golly SNS!")
	check(err, "SetBodyStr")
	msg.SetStrHeader("source", "sns-example")

	err = mgr.Send(u, msg)
	check(err, "Send")
	printSNSMsgId(msg)

	// Step 3: Send with a subject
	step("3. Send with a subject")
	msg2, _ := mgr.NewMessage("sns")
	_, _ = msg2.SetBodyStr("Your order #12345 has shipped!")
	opts := messaging.NewOptionsBuilder().
		Add(snsprovider.OptSubject, "Order Shipped").
		Build()
	err = mgr.Send(u, msg2, opts...)
	check(err, "Send with Subject")
	printSNSMsgId(msg2)

	// Step 4: Send a batch
	step("4. Send a batch of 5 messages")
	var msgs []messaging.Message
	for i := 0; i < 5; i++ {
		m, _ := mgr.NewMessage("sns")
		_, _ = m.SetBodyStr(fmt.Sprintf("Batch message %d", i+1))
		msgs = append(msgs, m)
	}
	err = mgr.SendBatch(u, msgs)
	check(err, "SendBatch")
	fmt.Printf("   Sent %d messages\n", len(msgs))

	// Step 5: Send using direct ARN URL
	step("5. Send using direct ARN URL")
	arnURL, _ := url.Parse(fmt.Sprintf("sns:///%s", topicARN))
	msg3, _ := mgr.NewMessage("sns")
	_, _ = msg3.SetBodyStr("Published via direct ARN")
	err = mgr.Send(arnURL, msg3)
	check(err, "Send via ARN")
	printSNSMsgId(msg3)

	// Step 6: Send large batch (auto-chunked)
	step("6. Send large batch of 15 (auto-chunked)")
	var largeBatch []messaging.Message
	for i := 0; i < 15; i++ {
		m, _ := mgr.NewMessage("sns")
		_, _ = m.SetBodyStr(fmt.Sprintf("Batch msg %d/15", i+1))
		largeBatch = append(largeBatch, m)
	}
	err = mgr.SendBatch(u, largeBatch)
	check(err, "SendBatch large")
	fmt.Printf("   Sent %d messages (auto-chunked)\n", len(largeBatch))

	// Step 7: Verify unsupported operations
	step("7. Verify unsupported operations")
	_, err = mgr.Receive(u)
	if err != nil {
		fmt.Printf("   Receive: %s\n", err)
	}
	_, err = mgr.ReceiveBatch(u)
	if err != nil {
		fmt.Printf("   ReceiveBatch: %s\n", err)
	}
	err = mgr.AddListener(u, func(msg messaging.Message) {})
	if err != nil {
		fmt.Printf("   AddListener: %s\n", err)
	}

	// Step 8: Close provider
	step("8. Close provider")
	err = mgr.Close()
	check(err, "Close")
	fmt.Println("   Closed (no-op for SNS)")

	// Step 9: Cleanup
	step("9. Cleanup")
	deleteTopic(cfg, topicARN)
	fmt.Printf("   Deleted topic: %s\n", topicARN)

	fmt.Println("\n=== Example Complete ===")
}

func createTopic(cfg *awscfg.Config, name string) string {
	awsCfg, err := cfg.LoadAWSConfig(context.Background())
	check(err, "LoadAWSConfig")
	client := sns.NewFromConfig(awsCfg, func(o *sns.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = &cfg.Endpoint
		}
	})
	output, err := client.CreateTopic(context.Background(), &sns.CreateTopicInput{Name: &name})
	check(err, "CreateTopic")
	return *output.TopicArn
}

func deleteTopic(cfg *awscfg.Config, topicARN string) {
	awsCfg, err := cfg.LoadAWSConfig(context.Background())
	check(err, "LoadAWSConfig")
	client := sns.NewFromConfig(awsCfg, func(o *sns.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = &cfg.Endpoint
		}
	})
	_, err = client.DeleteTopic(context.Background(), &sns.DeleteTopicInput{TopicArn: &topicARN})
	check(err, "DeleteTopic")
}

func printSNSMsgId(msg messaging.Message) {
	if snsMsg, ok := msg.(*snsprovider.MessageSNS); ok {
		mid := snsMsg.SNSMessageId()
		if mid != "" {
			fmt.Printf("   MessageId: %s\n", mid)
		} else {
			fmt.Println("   Published")
		}
	} else {
		fmt.Println("   Published")
	}
}

func step(name string) {
	fmt.Printf("-- %s --\n", name)
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
