package sns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"oss.nandlabs.io/golly-aws/awscfg"
	"oss.nandlabs.io/golly/messaging"
)

// -----------------------------------------------------------------------------
// Test helpers: httptest server that speaks the AWS SNS query protocol.
// SNS uses form-encoded POST bodies; we parse them and record the request
// values so the assertions can inspect e.g. MessageGroupId.
// -----------------------------------------------------------------------------

// captured records the parsed form values of a single request.
type captured struct {
	Form   url.Values
	Action string
}

// snsFakeServer wraps an httptest.Server plus a mutex-protected slice of
// captured requests. It responds with the minimal valid XML the SDK
// needs to decode Publish / PublishBatch / CreateTopic without error.
type snsFakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []captured
	// delay lets tests stall the handler so cancellation races can be
	// observed deterministically.
	delay time.Duration
}

func newSNSFakeServer() *snsFakeServer {
	s := &snsFakeServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *snsFakeServer) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(body))
	action := form.Get("Action")
	s.mu.Lock()
	s.requests = append(s.requests, captured{Form: form, Action: action})
	s.mu.Unlock()

	// Optional delay so the caller can cancel mid-flight.
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-r.Context().Done():
			// SDK aborted — nothing to write.
			return
		}
	}

	w.Header().Set("Content-Type", "text/xml")
	switch action {
	case "Publish":
		fmt.Fprint(w, `<PublishResponse xmlns="http://sns.amazonaws.com/doc/2010-03-31/">`+
			`<PublishResult><MessageId>test-msg-id</MessageId></PublishResult>`+
			`<ResponseMetadata><RequestId>req-1</RequestId></ResponseMetadata>`+
			`</PublishResponse>`)
	case "PublishBatch":
		// Echo back one Successful entry per input entry so PublishBatch
		// returns without a Failed slice. The SDK only needs Id.
		var successful strings.Builder
		i := 0
		for {
			key := fmt.Sprintf("PublishBatchRequestEntries.member.%d.Id", i+1)
			id := form.Get(key)
			if id == "" {
				break
			}
			fmt.Fprintf(&successful,
				`<member><Id>%s</Id><MessageId>mid-%d</MessageId></member>`,
				id, i)
			i++
		}
		fmt.Fprintf(w,
			`<PublishBatchResponse xmlns="http://sns.amazonaws.com/doc/2010-03-31/">`+
				`<PublishBatchResult><Successful>%s</Successful><Failed></Failed></PublishBatchResult>`+
				`<ResponseMetadata><RequestId>req-1</RequestId></ResponseMetadata>`+
				`</PublishBatchResponse>`, successful.String())
	case "CreateTopic":
		name := form.Get("Name")
		fmt.Fprintf(w,
			`<CreateTopicResponse xmlns="http://sns.amazonaws.com/doc/2010-03-31/">`+
				`<CreateTopicResult><TopicArn>arn:aws:sns:us-east-1:123456789012:%s</TopicArn></CreateTopicResult>`+
				`<ResponseMetadata><RequestId>req-1</RequestId></ResponseMetadata>`+
				`</CreateTopicResponse>`, name)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (s *snsFakeServer) captured() []captured {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]captured, len(s.requests))
	copy(out, s.requests)
	return out
}

// registerFakeSNS wires the fake server as the endpoint for the given
// awscfg registration key. Returns a cleanup that removes the config.
func registerFakeSNS(t *testing.T, key, endpoint string) {
	t.Helper()
	cfg := awscfg.NewConfig("us-east-1")
	cfg.SetStaticCredentials("test", "test", "")
	cfg.SetEndpoint(endpoint)
	awscfg.Manager.Register(key, cfg)
	t.Cleanup(func() { awscfg.Manager.Unregister(key) })
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestSendCtx_CancellationPropagates(t *testing.T) {
	srv := newSNSFakeServer()
	defer srv.Close()
	// Long enough that ctx cancellation is guaranteed to win the race.
	srv.delay = 2 * time.Second

	// Register both by ARN-derived key and by scheme name so lookup
	// resolves regardless of URL host layout.
	registerFakeSNS(t, "sns", srv.URL)

	p := &Provider{}
	msg, err := p.NewMessage(SNSScheme)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	_, _ = msg.SetBodyStr("hello")

	u, _ := url.Parse("sns:///arn:aws:sns:us-east-1:123456789012:my-topic")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = p.SendCtx(ctx, u, msg)
	if err == nil {
		t.Fatal("expected error from context cancellation, got nil")
	}
	// The SDK wraps the ctx error; unwrap to check.
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.DeadlineExceeded or Canceled in error chain, got %v", err)
	}
}

func TestSendCtx_KeyedMapsToMessageGroupId_OnFIFO(t *testing.T) {
	srv := newSNSFakeServer()
	defer srv.Close()
	registerFakeSNS(t, "sns", srv.URL)

	p := &Provider{}
	msg, err := p.NewMessage(SNSScheme)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	_, _ = msg.SetBodyStr("hello")
	// BaseMessage embeds messaging.Keyed; set the routing key.
	if k, ok := any(msg).(messaging.Keyed); ok {
		k.SetRoutingKey("tenant-42")
	} else {
		t.Fatal("expected message to implement messaging.Keyed")
	}

	// FIFO topic ARN — direct ARN URL to skip CreateTopic.
	u, _ := url.Parse("sns:///arn:aws:sns:us-east-1:123456789012:my-topic.fifo")

	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx: %v", err)
	}

	reqs := srv.captured()
	var pubReq *captured
	for i := range reqs {
		if reqs[i].Action == "Publish" {
			pubReq = &reqs[i]
			break
		}
	}
	if pubReq == nil {
		t.Fatalf("no Publish request captured; got %d requests", len(reqs))
	}
	got := pubReq.Form.Get("MessageGroupId")
	if got != "tenant-42" {
		t.Fatalf("expected MessageGroupId=tenant-42 on FIFO topic, got %q", got)
	}
}

func TestSendCtx_KeyedIgnoredOnStandard(t *testing.T) {
	srv := newSNSFakeServer()
	defer srv.Close()
	registerFakeSNS(t, "sns", srv.URL)

	p := &Provider{}
	msg, err := p.NewMessage(SNSScheme)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	_, _ = msg.SetBodyStr("hello")
	if k, ok := any(msg).(messaging.Keyed); ok {
		k.SetRoutingKey("tenant-42")
	}

	// Standard (non-FIFO) topic ARN.
	u, _ := url.Parse("sns:///arn:aws:sns:us-east-1:123456789012:my-topic")

	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx: %v", err)
	}

	reqs := srv.captured()
	var pubReq *captured
	for i := range reqs {
		if reqs[i].Action == "Publish" {
			pubReq = &reqs[i]
			break
		}
	}
	if pubReq == nil {
		t.Fatalf("no Publish request captured; got %d requests", len(reqs))
	}
	if got := pubReq.Form.Get("MessageGroupId"); got != "" {
		t.Fatalf("expected no MessageGroupId on standard topic, got %q", got)
	}
}

// recordingObserver captures OnSend invocations for assertions.
type recordingObserver struct {
	mu       sync.Mutex
	calls    int32
	lastURL  *url.URL
	lastMsg  messaging.Message
	lastErr  error
	lastElap time.Duration
}

func (o *recordingObserver) OnSend(u *url.URL, msg messaging.Message, err error, latency time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	atomic.AddInt32(&o.calls, 1)
	o.lastURL = u
	o.lastMsg = msg
	o.lastErr = err
	o.lastElap = latency
}
func (o *recordingObserver) OnReceive(*url.URL, messaging.Message, error) {}
func (o *recordingObserver) OnAck(*url.URL, messaging.Message)            {}
func (o *recordingObserver) OnNack(*url.URL, messaging.Message, bool)     {}

func TestObserver_OnSend_FiresWithLatency(t *testing.T) {
	srv := newSNSFakeServer()
	defer srv.Close()
	// A tiny handler delay so latency is provably positive.
	srv.delay = 20 * time.Millisecond
	registerFakeSNS(t, "sns", srv.URL)

	p := &Provider{}
	obs := &recordingObserver{}
	p.SetObserver(obs)

	msg, err := p.NewMessage(SNSScheme)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	_, _ = msg.SetBodyStr("hello")

	u, _ := url.Parse("sns:///arn:aws:sns:us-east-1:123456789012:my-topic")

	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx: %v", err)
	}

	obs.mu.Lock()
	defer obs.mu.Unlock()
	if got := atomic.LoadInt32(&obs.calls); got != 1 {
		t.Fatalf("expected 1 OnSend call, got %d", got)
	}
	if obs.lastErr != nil {
		t.Fatalf("expected nil err on OnSend, got %v", obs.lastErr)
	}
	if obs.lastURL == nil || obs.lastURL.String() != u.String() {
		t.Fatalf("expected URL %v, got %v", u, obs.lastURL)
	}
	if obs.lastMsg == nil {
		t.Fatal("expected non-nil msg on OnSend")
	}
	if obs.lastElap <= 0 {
		t.Fatalf("expected positive latency, got %v", obs.lastElap)
	}
}

func TestObserver_Nil_IsSafe(t *testing.T) {
	srv := newSNSFakeServer()
	defer srv.Close()
	registerFakeSNS(t, "sns", srv.URL)

	p := &Provider{}
	// Explicit nil after a real observer is set — clearing must be safe.
	p.SetObserver(&recordingObserver{})
	p.SetObserver(nil)

	msg, err := p.NewMessage(SNSScheme)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	_, _ = msg.SetBodyStr("hello")

	u, _ := url.Parse("sns:///arn:aws:sns:us-east-1:123456789012:my-topic")
	if err := p.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx with nil observer failed: %v", err)
	}

	// A brand-new provider that never had an observer set must also
	// send without panicking.
	fresh := &Provider{}
	if err := fresh.SendCtx(context.Background(), u, msg); err != nil {
		t.Fatalf("SendCtx on fresh provider (no observer) failed: %v", err)
	}
}
