package cognito

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"oss.nandlabs.io/golly/auth"
)

// Attribute names used in the DynamoDB item schema. The partition key
// is idAttr; the remaining attributes mirror auth.Session fields at the
// top level so operators can query them without hydrating the JSON
// payload.
//
// The table's partition key MUST be named "id" (string). The optional
// "expires_at" attribute holds a Unix timestamp in seconds and is
// suitable as the DynamoDB TTL attribute — operators should enable TTL
// on that field so expired sessions are pruned server-side.
const (
	idAttr        = "id"
	subjectAttr   = "user_id"
	payloadAttr   = "payload"
	expiresAtAttr = "expires_at"

	// GSIUserIDIndex is the name of the optional Global Secondary Index
	// that ListByUser queries. Create it with partition key user_id
	// (string) to enable per-user session listing.
	GSIUserIDIndex = "userID-index"
)

// ErrNotSupported is returned by ListByUser when the required GSI is
// absent on the table. The wrapped error carries the verbatim DynamoDB
// error so operators can diagnose without a round trip to CloudTrail.
var ErrNotSupported = errors.New("auth/cognito: operation not supported by current table schema")

// ddbAPI is the subset of the DynamoDB client used by the session
// store. Extracted so tests can substitute a mock without needing
// LocalStack or the DynamoDB emulator.
type ddbAPI interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// DynamoDBSessionStore persists auth.Session values in an Amazon
// DynamoDB table. Each session is serialized via json.Marshal into the
// payload attribute; user_id and expires_at mirror the session fields
// so that operators can query them directly (and so DynamoDB TTL can
// be enabled on expires_at). The table schema is:
//
//	partition key : id         (S)
//	attribute     : user_id    (S)  — mirror of Session.Subject
//	attribute     : payload    (S)  — JSON-encoded Session
//	attribute     : expires_at (N)  — Unix seconds; use as TTL attribute
//
// Callers who want ListByUser must also create a Global Secondary Index
// named GSIUserIDIndex with partition key user_id (S).
type DynamoDBSessionStore struct {
	api     ddbAPI
	table   string
	timeout time.Duration
}

// NewDynamoDBSessionStore constructs a session store backed by the
// given DynamoDB client and table name. The returned type satisfies
// auth.SessionStore and additionally exposes ListByUser.
//
// The caller is responsible for creating the table with the schema
// documented on DynamoDBSessionStore. To auto-expire sessions, enable
// DynamoDB TTL on the "expires_at" attribute (AWS Console → Table →
// Additional settings → TTL attribute name = "expires_at").
func NewDynamoDBSessionStore(client *dynamodb.Client, table string) *DynamoDBSessionStore {
	if client == nil {
		panic("auth/cognito: nil dynamodb.Client")
	}
	if table == "" {
		panic("auth/cognito: empty table name")
	}
	return &DynamoDBSessionStore{api: client, table: table}
}

// newSessionStoreWithBackend is the unit-test constructor; production
// callers should use NewDynamoDBSessionStore.
func newSessionStoreWithBackend(api ddbAPI, table string) *DynamoDBSessionStore {
	return &DynamoDBSessionStore{api: api, table: table}
}

// WithTimeout sets a per-call context deadline applied to every RPC.
// A non-positive value clears the timeout.
func (s *DynamoDBSessionStore) WithTimeout(d time.Duration) *DynamoDBSessionStore {
	s.timeout = d
	return s
}

// Get implements auth.SessionStore. Returns (nil, false) when the id is
// unknown or on any transport error — the interface has no error
// channel, so failures are silently swallowed (add a wrapping
// SessionStore in your app if you need error observability).
func (s *DynamoDBSessionStore) Get(id string) (*auth.Session, bool) {
	if id == "" {
		return nil, false
	}
	ctx, cancel := s.ctx()
	defer cancel()
	out, err := s.api.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			idAttr: &ddbtypes.AttributeValueMemberS{Value: id},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil || out == nil || len(out.Item) == 0 {
		return nil, false
	}
	sess, err := decodeSession(out.Item)
	if err != nil {
		return nil, false
	}
	return sess, true
}

// Put implements auth.SessionStore.
func (s *DynamoDBSessionStore) Put(sess *auth.Session) error {
	if sess == nil || sess.ID == "" {
		return errors.New("auth/cognito: empty session id")
	}
	item, err := encodeSession(sess)
	if err != nil {
		return err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	_, err = s.api.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item:      item,
	})
	return err
}

// Delete implements auth.SessionStore. Missing items are treated as a
// success — callers of SessionStore.Delete typically already treat
// idempotent revocation as the desired shape.
func (s *DynamoDBSessionStore) Delete(id string) error {
	if id == "" {
		return errors.New("auth/cognito: empty session id")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.api.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.table),
		Key: map[string]ddbtypes.AttributeValue{
			idAttr: &ddbtypes.AttributeValueMemberS{Value: id},
		},
	})
	return err
}

// ListByUser returns every session whose Subject equals uid. It queries
// the Global Secondary Index named GSIUserIDIndex; if that index is
// missing on the table, DynamoDB returns ValidationException — this
// method surfaces the underlying error wrapped in ErrNotSupported with
// a hint about the required GSI so operators can act on it.
func (s *DynamoDBSessionStore) ListByUser(ctx context.Context, uid string) ([]*auth.Session, error) {
	if uid == "" {
		return nil, errors.New("auth/cognito: empty user id")
	}
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	out, err := s.api.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		IndexName:              aws.String(GSIUserIDIndex),
		KeyConditionExpression: aws.String("#u = :uid"),
		ExpressionAttributeNames: map[string]string{
			"#u": subjectAttr,
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":uid": &ddbtypes.AttributeValueMemberS{Value: uid},
		},
	})
	if err != nil {
		// DynamoDB returns ValidationException when the referenced
		// index does not exist. Surface the raw error alongside
		// ErrNotSupported so callers can errors.Is-check and still
		// see what really happened.
		return nil, fmt.Errorf("%w: create GSI %q with partition key %q (string) to enable ListByUser: %v",
			ErrNotSupported, GSIUserIDIndex, subjectAttr, err)
	}
	sessions := make([]*auth.Session, 0, len(out.Items))
	for _, item := range out.Items {
		sess, err := decodeSession(item)
		if err != nil {
			return nil, fmt.Errorf("auth/cognito: decode item for uid %q: %w", uid, err)
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

// ctx returns a context that carries the configured timeout (or a
// no-op cancel when timeout is 0).
func (s *DynamoDBSessionStore) ctx() (context.Context, context.CancelFunc) {
	if s.timeout <= 0 {
		return context.Background(), func() {}
	}
	return context.WithTimeout(context.Background(), s.timeout)
}

// encodeSession marshals a session into a DynamoDB item using typed
// attributes: id (S), user_id (S), payload (S), expires_at (N seconds).
func encodeSession(sess *auth.Session) (map[string]ddbtypes.AttributeValue, error) {
	b, err := json.Marshal(sess)
	if err != nil {
		return nil, fmt.Errorf("auth/cognito: marshal session: %w", err)
	}
	item := map[string]ddbtypes.AttributeValue{
		idAttr:      &ddbtypes.AttributeValueMemberS{Value: sess.ID},
		subjectAttr: &ddbtypes.AttributeValueMemberS{Value: sess.Subject},
		payloadAttr: &ddbtypes.AttributeValueMemberS{Value: string(b)},
	}
	if !sess.ExpiresAt.IsZero() {
		item[expiresAtAttr] = &ddbtypes.AttributeValueMemberN{
			Value: strconv.FormatInt(sess.ExpiresAt.Unix(), 10),
		}
	}
	return item, nil
}

// decodeSession recovers a session from a DynamoDB item. The payload
// attribute holds the JSON round-trip source of truth; mirrored
// user_id/expires_at fields are ignored on read.
func decodeSession(item map[string]ddbtypes.AttributeValue) (*auth.Session, error) {
	raw, ok := item[payloadAttr]
	if !ok {
		return nil, fmt.Errorf("auth/cognito: item missing %q attribute", payloadAttr)
	}
	var payload []byte
	switch v := raw.(type) {
	case *ddbtypes.AttributeValueMemberS:
		payload = []byte(v.Value)
	case *ddbtypes.AttributeValueMemberB:
		payload = v.Value
	default:
		return nil, fmt.Errorf("auth/cognito: %q attribute has unsupported type %T", payloadAttr, raw)
	}
	var sess auth.Session
	if err := json.Unmarshal(payload, &sess); err != nil {
		return nil, fmt.Errorf("auth/cognito: unmarshal session: %w", err)
	}
	return &sess, nil
}
