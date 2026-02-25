package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"oss.nandlabs.io/golly-aws/awscfg"
	_ "oss.nandlabs.io/golly-aws/sqs"
	"oss.nandlabs.io/golly/messaging"
)

func main() {
	region := envOrDefault("AWS_REGION", "us-east-1")
	queueName := envOrDefault("SQS_QUEUE", "golly-sqs-example")
	endpoint := os.Getenv("SQS_ENDPOINT")

	cfg := awscfg.NewConfig(region)
	if endpoint != "" {
		cfg.SetEndpoint(endpoint)
		cfg.SetStaticCredentials("test", "test", "")
		fmt.Printf("Using custom endpoint: %s\n", endpoint)
	}
	awscfg.Manager.Register("sqs", cfg)

	fmt.Println("=== golly SQS Example ===")
	fmt.Printf("Queue: %s | Region: %s\n\n", queueName, region)

	step("1. Create SQS queue")
	queueURL := createQueue(cfg, queueName)
	fmt.Printf("   Queue URL: %s\n", queueURL)

	mgr := messaging.GetManager()
	setupSignalHandler(mgr)
	u, _ := url.Parse(fmt.Sprintf("sqs://%s", queueName))

	step("2. Send a single message")
	msg, err := mgr.NewMessage("sqs")
	check(err, "NewMessage")
	_, err = msg.SetBodyStr("Hello from golly SQS!")
	check(err, "SetBodyStr")
	msg.SetStrHeader("source", "sqs-example")
	msg.SetStrHeader("env", "development")
	err = mgr.Send(u, msg)
	check(err, "Send")
	fmt.Println("   Message sent successfully")

	step("3. Receive a single message")
	opts := messaging.NewOptionsBuilder().
		Add("WaitTimeSeconds", 5).
		Build()
	received, err := mgr.Receive(u, opts...)
	check(err, "Receive")
	fmt.Printf("   Body:   %s\n", received.ReadAsStr())
	if src, ok := received.GetStrHeader("source"); ok {
		fmt.Printf("   Header: source=%s\n", src)
	}
	err = received.Rsvp(true)
	check(err, "Rsvp(true)")
	fmt.Println("   Message acknowledged (deleted)")

	step("4. Send a batch of 15 messages (auto-split into 2 batches)")
	var msgs []messaging.Message
	for i := 1; i <= 15; i++ {
		m, err := mgr.NewMessage("sqs")
		check(err, "NewMessage batch")
		_, err = m.SetBodyStr(fmt.Sprintf("Batch message #%d", i))
		check(err, "SetBodyStr batch")
		m.SetStrHeader("index", fmt.Sprintf("%d", i))
		msgs = append(msgs, m)
	}
	err = mgr.SendBatch(u, msgs)
	check(err, "SendBatch")
	fmt.Println("   15 messages sent in batches of 10")

	step("5. Receive a batch of messages")
	batchOpts := messaging.NewOptionsBuilder().
		Add("BatchSize", 5).
		Add("WaitTimeSeconds", 5).
		Build()
	batchMsgs, err := mgr.ReceiveBatch(u, batchOpts...)
	check(err, "ReceiveBatch")
	fmt.Printf("   Received %d messages:\n", len(batchMsgs))
	for i, m := range batchMsgs {
		fmt.Printf("     [%d] %s\n", i+1, m.ReadAsStr())
		_ = m.Rsvp(true)
	}

	step("6. Send and receive a JSON message")
	jsonMsg, err := mgr.NewMessage("sqs")
	check(err, "NewMessage JSON")
	order := map[string]interface{}{
		"orderId": "ORD-12345",
		"total":   99.99,
		"items":   3,
	}
	err = jsonMsg.WriteJSON(order)
	check(err, "WriteJSON")
	jsonMsg.SetStrHeader("content-type", "application/json")
	err = mgr.Send(u, jsonMsg)
	check(err, "Send JSON")
	fmt.Println("   JSON message sent")
	jsonReceived, err := mgr.Receive(u, opts...)
	check(err, "Receive JSON")
	fmt.Printf("   JSON body: %s\n", jsonReceived.ReadAsStr())
	_ = jsonReceived.Rsvp(true)

	step("7. Send a message with 2-second delay")
	delayMsg, err := mgr.NewMessage("sqs")
	check(err, "NewMessage delay")
	_, err = delayMsg.SetBodyStr("This message was delayed by 2 seconds")
	check(err, "SetBodyStr delay")
	delayOpts := messaging.NewOptionsBuilder().
		Add("DelaySeconds", 2).
		Build()
	err = mgr.Send(u, delayMsg, delayOpts...)
	check(err, "Send delayed")
	fmt.Println("   Delayed message sent (2s delay)")

	step("8. Demonstrate message rejection (Rsvp false)")
	rejectMsg, err := mgr.NewMessage("sqs")
	check(err, "NewMessage reject")
	_, err = rejectMsg.SetBodyStr("This message will be rejected once")
	check(err, "SetBodyStr reject")
	err = mgr.Send(u, rejectMsg)
	check(err, "Send reject")
	time.Sleep(1 * time.Second)
	rejReceived, err := mgr.Receive(u, opts...)
	check(err, "Receive for reject")
	fmt.Printf("   Received: %s\n", rejReceived.ReadAsStr())
	err = rejReceived.Rsvp(false)
	check(err, "Rsvp(false)")
	fmt.Println("   Message rejected (visibility reset to 0)")
	fmt.Println("   Message is now available for reprocessing")
	time.Sleep(1 * time.Second)
	reReceived, err := mgr.Receive(u, opts...)
	if err == nil {
		fmt.Printf("   Re-received: %s\n", reReceived.ReadAsStr())
		_ = reReceived.Rsvp(true)
		fmt.Println("   Message acknowledged on second attempt")
	} else {
		fmt.Printf("   (message not yet visible: %v)\n", err)
	}

	step("9. Add a listener (polls for 10 seconds)")
	var wg sync.WaitGroup
	listenerCount := 0
	for i := 1; i <= 5; i++ {
		m, _ := mgr.NewMessage("sqs")
		_, _ = m.SetBodyStr(fmt.Sprintf("Listener message #%d", i))
		_ = mgr.Send(u, m)
	}
	fmt.Println("   Sent 5 messages for the listener")
	listenerOpts := messaging.NewOptionsBuilder().
		Add("WaitTimeSeconds", 2).
		Add("Timeout", 10).
		Build()
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(12 * time.Second)
	}()
	err = mgr.AddListener(u, func(msg messaging.Message) {
		listenerCount++
		fmt.Printf("   [Listener] #%d: %s\n", listenerCount, msg.ReadAsStr())
		_ = msg.Rsvp(true)
	}, listenerOpts...)
	check(err, "AddListener")
	fmt.Println("   Listener started (10s timeout)...")
	wg.Wait()
	fmt.Printf("   Listener processed %d messages\n", listenerCount)

	step("10. Drain remaining messages from queue")
	drainOpts := messaging.NewOptionsBuilder().
		Add("WaitTimeSeconds", 2).
		Add("BatchSize", 10).
		Build()
	drained := 0
	for {
		batch, err := mgr.ReceiveBatch(u, drainOpts...)
		if err != nil {
			break
		}
		for _, m := range batch {
			_ = m.Rsvp(true)
			drained++
		}
	}
	fmt.Printf("   Drained %d remaining messages\n", drained)

	step("11. Graceful shutdown")
	err = mgr.Close()
	check(err, "Close")
	fmt.Println("   Manager closed, all listeners stopped")

	step("12. Delete queue (cleanup)")
	deleteQueue(cfg, queueURL)
	fmt.Printf("   Queue %s deleted\n", queueName)

	fmt.Println("\n=== All SQS examples completed successfully! ===")
}

func createQueue(cfg *awscfg.Config, name string) string {
	client := newSQSClient(cfg)
	output, err := client.CreateQueue(context.Background(), &sqs.CreateQueueInput{
		QueueName: &name,
	})
	if err != nil {
		log.Fatalf("Failed to create queue %q: %v", name, err)
	}
	return *output.QueueUrl
}

func deleteQueue(cfg *awscfg.Config, queueURL string) {
	client := newSQSClient(cfg)
	_, err := client.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{
		QueueUrl: &queueURL,
	})
	if err != nil {
		log.Printf("Warning: failed to delete queue: %v", err)
	}
}

func newSQSClient(cfg *awscfg.Config) *sqs.Client {
	awsCfg, err := cfg.LoadAWSConfig(context.Background())
	if err != nil {
		log.Fatalf("Failed to load AWS config: %v", err)
	}
	var sqsOpts []func(*sqs.Options)
	if cfg.Endpoint != "" {
		sqsOpts = append(sqsOpts, func(o *sqs.Options) {
			o.BaseEndpoint = &cfg.Endpoint
		})
	}
	return sqs.NewFromConfig(awsCfg, sqsOpts...)
}

func step(name string) {
	fmt.Printf("\n-- %s --\n", name)
}

func check(err error, ctx string) {
	if err != nil {
		log.Fatalf("[ERROR] %s: %v", ctx, err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func setupSignalHandler(mgr messaging.Manager) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Printf("\nReceived %s, shutting down...\n", sig)
		_ = mgr.Close()
		os.Exit(0)
	}()
}
