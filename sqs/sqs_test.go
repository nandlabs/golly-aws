package sqs

import (
	"net/url"
	"testing"

	"oss.nandlabs.io/golly-aws/awscfg"
	"oss.nandlabs.io/golly/messaging"
)

func TestProviderSchemes(t *testing.T) {
	p := &Provider{}
	schemes := p.Schemes()
	if len(schemes) != 1 {
		t.Fatalf("expected 1 scheme, got %d", len(schemes))
	}
	if schemes[0] != SQSScheme {
		t.Fatalf("expected scheme %q, got %q", SQSScheme, schemes[0])
	}
}

func TestProviderID(t *testing.T) {
	p := &Provider{}
	if p.Id() != SQSProviderID {
		t.Fatalf("expected id %q, got %q", SQSProviderID, p.Id())
	}
}

func TestProviderSetup(t *testing.T) {
	p := &Provider{}
	if err := p.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
}

func TestNewMessage(t *testing.T) {
	p := &Provider{}
	msg, err := p.NewMessage(SQSScheme)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Id() == "" {
		t.Fatal("expected non-empty message ID")
	}
}

func TestNewMessageBody(t *testing.T) {
	p := &Provider{}
	msg, err := p.NewMessage(SQSScheme)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	body := "test message body"
	_, err = msg.SetBodyStr(body)
	if err != nil {
		t.Fatalf("SetBodyStr failed: %v", err)
	}

	if msg.ReadAsStr() != body {
		t.Fatalf("expected body %q, got %q", body, msg.ReadAsStr())
	}
}

func TestNewMessageHeaders(t *testing.T) {
	p := &Provider{}
	msg, err := p.NewMessage(SQSScheme)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	msg.SetStrHeader("key1", "value1")
	msg.SetIntHeader("key2", 42)
	msg.SetBoolHeader("key3", true)

	v1, ok := msg.GetStrHeader("key1")
	if !ok || v1 != "value1" {
		t.Fatalf("expected header key1=value1, got %q exists=%t", v1, ok)
	}

	v2, ok := msg.GetIntHeader("key2")
	if !ok || v2 != 42 {
		t.Fatalf("expected header key2=42, got %d exists=%t", v2, ok)
	}

	v3, ok := msg.GetBoolHeader("key3")
	if !ok || !v3 {
		t.Fatalf("expected header key3=true, got %t exists=%t", v3, ok)
	}
}

func TestMessageSQSRsvpNoProvider(t *testing.T) {
	baseMsg, err := messaging.NewBaseMessage()
	if err != nil {
		t.Fatalf("NewBaseMessage failed: %v", err)
	}
	msg := &MessageSQS{
		BaseMessage: baseMsg,
		provider:    nil,
	}
	// Rsvp with nil provider should not panic
	err = msg.Rsvp(true)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestProviderClose(t *testing.T) {
	p := &Provider{}
	if err := p.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestResolveQueueURLWithEndpoint(t *testing.T) {
	cfg := awscfg.NewConfig("us-east-1")
	cfg.SetEndpoint("http://localhost:4566")
	cfg.SetStaticCredentials("test", "test", "")
	awscfg.Manager.Register("test-queue", cfg)

	u, _ := url.Parse("sqs://test-queue")
	queueURL, err := resolveQueueURL(nil, u)
	if err != nil {
		t.Fatalf("resolveQueueURL failed: %v", err)
	}
	expected := "http://localhost:4566/000000000000/test-queue"
	if queueURL != expected {
		t.Fatalf("expected %q, got %q", expected, queueURL)
	}
}

func TestResolveQueueURLWithAccountID(t *testing.T) {
	cfg := awscfg.NewConfig("us-east-1")
	cfg.SetEndpoint("http://localhost:4566")
	cfg.SetStaticCredentials("test", "test", "")
	awscfg.Manager.Register("test-queue-2", cfg)

	u, _ := url.Parse("sqs://test-queue-2/123456789012")
	queueURL, err := resolveQueueURL(nil, u)
	if err != nil {
		t.Fatalf("resolveQueueURL failed: %v", err)
	}
	expected := "http://localhost:4566/123456789012/test-queue-2"
	if queueURL != expected {
		t.Fatalf("expected %q, got %q", expected, queueURL)
	}
}

func TestResolveQueueURLNoHost(t *testing.T) {
	u, _ := url.Parse("sqs:///")
	_, err := resolveQueueURL(nil, u)
	if err == nil {
		t.Fatal("expected error for empty host")
	}
}

func TestGetSQSClientWithConfig(t *testing.T) {
	cfg := awscfg.NewConfig("us-west-2")
	cfg.SetStaticCredentials("test", "test", "")
	cfg.SetEndpoint("http://localhost:4566")
	awscfg.Manager.Register("sqs", cfg)

	u, _ := url.Parse("sqs://my-queue")
	client, err := getSQSClient(u)
	if err != nil {
		t.Fatalf("getSQSClient failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestGetSQSClientFallback(t *testing.T) {
	// Ensure fallback works when no config is registered for the specific name
	u, _ := url.Parse("sqs://unregistered-queue-name-12345")
	client, err := getSQSClient(u)
	if err != nil {
		t.Fatalf("getSQSClient fallback failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client from fallback")
	}
}

func TestProviderRegistered(t *testing.T) {
	mgr := messaging.GetManager()
	schemes := mgr.Schemes()
	found := false
	for _, s := range schemes {
		if s == SQSScheme {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("SQS scheme %q not found in manager schemes: %v", SQSScheme, schemes)
	}
}

func TestBuildMessageAttributes(t *testing.T) {
	p := &Provider{}
	msg, _ := p.NewMessage(SQSScheme)
	attrs := buildMessageAttributes(msg)
	// Currently returns nil - just verify no panic
	if attrs != nil {
		t.Fatal("expected nil attributes")
	}
}

func TestStrPtr(t *testing.T) {
	s := "hello"
	ptr := strPtr(s)
	if *ptr != s {
		t.Fatalf("expected %q, got %q", s, *ptr)
	}
}
