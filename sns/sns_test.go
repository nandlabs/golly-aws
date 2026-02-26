package sns

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
	if schemes[0] != SNSScheme {
		t.Fatalf("expected scheme %q, got %q", SNSScheme, schemes[0])
	}
}

func TestProviderID(t *testing.T) {
	p := &Provider{}
	if p.Id() != SNSProviderID {
		t.Fatalf("expected id %q, got %q", SNSProviderID, p.Id())
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
	msg, err := p.NewMessage(SNSScheme)
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
	msg, err := p.NewMessage(SNSScheme)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}
	body := "test SNS message body"
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
	msg, err := p.NewMessage(SNSScheme)
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}
	msg.SetStrHeader("key1", "value1")
	msg.SetIntHeader("key2", 42)
	msg.SetBoolHeader("key3", true)

	v1, ok := msg.GetStrHeader("key1")
	if !ok || v1 != "value1" {
		t.Fatalf("expected key1=value1, got %q exists=%t", v1, ok)
	}
	v2, ok := msg.GetIntHeader("key2")
	if !ok || v2 != 42 {
		t.Fatalf("expected key2=42, got %d exists=%t", v2, ok)
	}
	v3, ok := msg.GetBoolHeader("key3")
	if !ok || !v3 {
		t.Fatalf("expected key3=true, got %t exists=%t", v3, ok)
	}
}

func TestMessageSNSRsvp(t *testing.T) {
	baseMsg, err := messaging.NewBaseMessage()
	if err != nil {
		t.Fatalf("NewBaseMessage failed: %v", err)
	}
	msg := &MessageSNS{BaseMessage: baseMsg, provider: nil}
	err = msg.Rsvp(true)
	if err != nil {
		t.Fatalf("Rsvp(true) expected nil, got: %v", err)
	}
	err = msg.Rsvp(false)
	if err != nil {
		t.Fatalf("Rsvp(false) expected nil, got: %v", err)
	}
}

func TestMessageSNSId(t *testing.T) {
	msg := &MessageSNS{messageId: "test-sns-id-123"}
	if msg.SNSMessageId() != "test-sns-id-123" {
		t.Fatalf("expected %q, got %q", "test-sns-id-123", msg.SNSMessageId())
	}
}

func TestMessageSNSIdEmpty(t *testing.T) {
	msg := &MessageSNS{}
	if msg.SNSMessageId() != "" {
		t.Fatalf("expected empty, got %q", msg.SNSMessageId())
	}
}

func TestProviderClose(t *testing.T) {
	p := &Provider{}
	if err := p.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestReceiveUnsupported(t *testing.T) {
	p := &Provider{}
	u, _ := url.Parse("sns://my-topic")
	_, err := p.Receive(u)
	if err == nil {
		t.Fatal("expected error for Receive on SNS")
	}
}

func TestReceiveBatchUnsupported(t *testing.T) {
	p := &Provider{}
	u, _ := url.Parse("sns://my-topic")
	_, err := p.ReceiveBatch(u)
	if err == nil {
		t.Fatal("expected error for ReceiveBatch on SNS")
	}
}

func TestAddListenerUnsupported(t *testing.T) {
	p := &Provider{}
	u, _ := url.Parse("sns://my-topic")
	err := p.AddListener(u, func(msg messaging.Message) {})
	if err == nil {
		t.Fatal("expected error for AddListener on SNS")
	}
}

func TestResolveTopicARNDirect(t *testing.T) {
	arn := "arn:aws:sns:us-east-1:123456789012:my-topic"
	u, _ := url.Parse("sns:///" + arn)
	resolved, err := resolveTopicARN(nil, u)
	if err != nil {
		t.Fatalf("resolveTopicARN failed: %v", err)
	}
	if resolved != arn {
		t.Fatalf("expected %q, got %q", arn, resolved)
	}
}

func TestResolveTopicARNEmptyPath(t *testing.T) {
	u, _ := url.Parse("sns:///")
	_, err := resolveTopicARN(nil, u)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestResolveTopicARNNoHost(t *testing.T) {
	u := &url.URL{Scheme: "sns"}
	_, err := resolveTopicARN(nil, u)
	if err == nil {
		t.Fatal("expected error for empty host and path")
	}
}

func TestGetSNSClientWithConfig(t *testing.T) {
	cfg := awscfg.NewConfig("us-west-2")
	cfg.SetStaticCredentials("test", "test", "")
	cfg.SetEndpoint("http://localhost:4566")
	awscfg.Manager.Register("sns", cfg)

	u, _ := url.Parse("sns://my-topic")
	client, err := getSNSClient(u)
	if err != nil {
		t.Fatalf("getSNSClient failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestGetSNSClientFallback(t *testing.T) {
	u, _ := url.Parse("sns://unregistered-topic-name-12345")
	client, err := getSNSClient(u)
	if err != nil {
		t.Fatalf("getSNSClient fallback failed: %v", err)
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
		if s == SNSScheme {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("SNS scheme %q not found in manager schemes: %v", SNSScheme, schemes)
	}
}

func TestBuildMessageAttributes(t *testing.T) {
	p := &Provider{}
	msg, _ := p.NewMessage(SNSScheme)
	attrs := buildMessageAttributes(msg)
	if attrs != nil {
		t.Fatal("expected nil attributes")
	}
}

func TestBuildBatchMessageAttributes(t *testing.T) {
	p := &Provider{}
	msg, _ := p.NewMessage(SNSScheme)
	attrs := buildBatchMessageAttributes(msg)
	if attrs != nil {
		t.Fatal("expected nil batch attributes")
	}
}

func TestStrPtr(t *testing.T) {
	s := "hello"
	ptr := strPtr(s)
	if *ptr != s {
		t.Fatalf("expected %q, got %q", s, *ptr)
	}
}

func TestSafeDeref(t *testing.T) {
	s := "test"
	if safeDeref(&s) != "test" {
		t.Fatal("expected 'test'")
	}
	if safeDeref(nil) != "" {
		t.Fatal("expected empty string for nil")
	}
}

func TestSendBatchEmpty(t *testing.T) {
	p := &Provider{}
	u, _ := url.Parse("sns://my-topic")
	err := p.SendBatch(u, nil)
	if err != nil {
		t.Fatalf("expected nil error for empty batch, got: %v", err)
	}
}
