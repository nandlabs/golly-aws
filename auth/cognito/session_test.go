package cognito

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"oss.nandlabs.io/golly/auth"
)

// memDDB is an in-memory ddbAPI that mirrors DynamoDB's Item shape
// (map[string]AttributeValue per row) so it exercises the same
// encode/decode paths as the real client. Query filters on user_id
// (the mirrored subject attribute) — matching the GSI schema
// documented on DynamoDBSessionStore.
type memDDB struct {
	mu     sync.Mutex
	table  string
	items  map[string]map[string]ddbtypes.AttributeValue
	failOn string // when set, matching op returns errBoom
	// Optional Query hook so tests can simulate ValidationException
	// (missing GSI) surfacing through ListByUser.
	queryErr error
}

var errBoom = errors.New("boom")

func newMemDDB(table string) *memDDB {
	return &memDDB{
		table: table,
		items: make(map[string]map[string]ddbtypes.AttributeValue),
	}
}

func (m *memDDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failOn == "GetItem" {
		return nil, errBoom
	}
	if aws.ToString(in.TableName) != m.table {
		return nil, errors.New("wrong table")
	}
	idAV, ok := in.Key[idAttr].(*ddbtypes.AttributeValueMemberS)
	if !ok {
		return nil, errors.New("bad key")
	}
	item, ok := m.items[idAV.Value]
	if !ok {
		return &dynamodb.GetItemOutput{}, nil
	}
	// Return a clone so mutations by the caller don't leak into the store.
	out := make(map[string]ddbtypes.AttributeValue, len(item))
	for k, v := range item {
		out[k] = v
	}
	return &dynamodb.GetItemOutput{Item: out}, nil
}

func (m *memDDB) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failOn == "PutItem" {
		return nil, errBoom
	}
	if aws.ToString(in.TableName) != m.table {
		return nil, errors.New("wrong table")
	}
	idAV, ok := in.Item[idAttr].(*ddbtypes.AttributeValueMemberS)
	if !ok {
		return nil, errors.New("missing id attr")
	}
	clone := make(map[string]ddbtypes.AttributeValue, len(in.Item))
	for k, v := range in.Item {
		clone[k] = v
	}
	m.items[idAV.Value] = clone
	return &dynamodb.PutItemOutput{}, nil
}

func (m *memDDB) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failOn == "DeleteItem" {
		return nil, errBoom
	}
	if aws.ToString(in.TableName) != m.table {
		return nil, errors.New("wrong table")
	}
	idAV, ok := in.Key[idAttr].(*ddbtypes.AttributeValueMemberS)
	if !ok {
		return nil, errors.New("bad key")
	}
	delete(m.items, idAV.Value)
	return &dynamodb.DeleteItemOutput{}, nil
}

func (m *memDDB) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	if aws.ToString(in.IndexName) != GSIUserIDIndex {
		return nil, errors.New("query targets unexpected index: " + aws.ToString(in.IndexName))
	}
	uidAV, ok := in.ExpressionAttributeValues[":uid"].(*ddbtypes.AttributeValueMemberS)
	if !ok {
		return nil, errors.New("missing :uid binding")
	}
	var out []map[string]ddbtypes.AttributeValue
	for _, item := range m.items {
		sub, ok := item[subjectAttr].(*ddbtypes.AttributeValueMemberS)
		if !ok {
			continue
		}
		if sub.Value == uidAV.Value {
			out = append(out, item)
		}
	}
	return &dynamodb.QueryOutput{Items: out, Count: int32(len(out))}, nil
}

func newTestStore() (*DynamoDBSessionStore, *memDDB) {
	backend := newMemDDB("sessions")
	return newSessionStoreWithBackend(backend, "sessions"), backend
}

func mkSession(id, sub string) *auth.Session {
	now := time.Now().UTC().Truncate(time.Second)
	return &auth.Session{
		ID:        id,
		Subject:   sub,
		CreatedAt: now,
		LastSeen:  now,
		ExpiresAt: now.Add(time.Hour),
		Data:      map[string]any{"role": "user"},
	}
}

func TestSessionStore_PutGetRoundTrip(t *testing.T) {
	s, _ := newTestStore()
	sess := mkSession("sid-1", "user-1")
	if err := s.Put(sess); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("Get: not found after Put")
	}
	if got.ID != sess.ID || got.Subject != sess.Subject {
		t.Errorf("got %+v want %+v", got, sess)
	}
	if !got.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Errorf("ExpiresAt: got %v want %v", got.ExpiresAt, sess.ExpiresAt)
	}
	if got.Data["role"] != "user" {
		t.Errorf("Data lost in round trip: %+v", got.Data)
	}
}

func TestSessionStore_GetMissing(t *testing.T) {
	s, _ := newTestStore()
	if _, ok := s.Get("nope"); ok {
		t.Fatal("Get on empty store returned ok")
	}
	if _, ok := s.Get(""); ok {
		t.Fatal("Get(\"\") should return false")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	s, _ := newTestStore()
	if err := s.Put(mkSession("sid-1", "user-1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("sid-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := s.Get("sid-1"); ok {
		t.Fatal("session still present after Delete")
	}
	// Idempotent: deleting a missing session is not an error.
	if err := s.Delete("sid-1"); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
	if err := s.Delete(""); err == nil {
		t.Fatal("Delete(\"\") should error on empty id")
	}
}

func TestSessionStore_PutEmptyIDFails(t *testing.T) {
	s, _ := newTestStore()
	if err := s.Put(&auth.Session{ID: ""}); err == nil {
		t.Fatal("Put with empty id: expected error")
	}
	if err := s.Put(nil); err == nil {
		t.Fatal("Put(nil): expected error")
	}
}

func TestSessionStore_ListByUser(t *testing.T) {
	s, _ := newTestStore()
	for i, tc := range []struct {
		id, sub string
	}{
		{"a", "user-1"},
		{"b", "user-1"},
		{"c", "user-2"},
	} {
		if err := s.Put(mkSession(tc.id, tc.sub)); err != nil {
			t.Fatalf("Put[%d]: %v", i, err)
		}
	}
	got, err := s.ListByUser(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	ids := make([]string, len(got))
	for i, g := range got {
		ids[i] = g.ID
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Errorf("ListByUser user-1: got %v want [a b]", ids)
	}

	empty, err := s.ListByUser(context.Background(), "user-3")
	if err != nil {
		t.Fatalf("ListByUser (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ListByUser user-3: got %d, want 0", len(empty))
	}

	if _, err := s.ListByUser(context.Background(), ""); err == nil {
		t.Fatal("ListByUser(\"\"): expected error")
	}
}

func TestSessionStore_ListByUser_MissingGSI(t *testing.T) {
	s, backend := newTestStore()
	// Simulate DynamoDB's ValidationException when the target index is
	// not defined on the table.
	backend.queryErr = errors.New("ValidationException: The table does not have the specified index: userID-index")

	_, err := s.ListByUser(context.Background(), "user-1")
	if err == nil {
		t.Fatal("expected error from ListByUser when GSI is missing")
	}
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("expected ErrNotSupported wrap; got %v", err)
	}
	if got := err.Error(); !contains(got, "userID-index") || !contains(got, "ValidationException") {
		t.Errorf("wrapped error should include index name and DynamoDB message; got %q", got)
	}
}

func TestSessionStore_PayloadIsJSON(t *testing.T) {
	// Confirm the stored item really uses json.Marshal — inspect the
	// payload attribute directly through the in-memory backend.
	s, backend := newTestStore()
	sess := mkSession("sid-1", "user-1")
	if err := s.Put(sess); err != nil {
		t.Fatal(err)
	}
	item := backend.items[sess.ID]
	raw, ok := item[payloadAttr].(*ddbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatalf("payload attribute missing or wrong type; item=%v", item)
	}
	var round auth.Session
	if err := json.Unmarshal([]byte(raw.Value), &round); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if round.Subject != sess.Subject {
		t.Errorf("json round-trip subject: got %q want %q", round.Subject, sess.Subject)
	}
	if s2, ok := item[subjectAttr].(*ddbtypes.AttributeValueMemberS); !ok || s2.Value != sess.Subject {
		t.Errorf("mirrored user_id attribute: got %v want %q", item[subjectAttr], sess.Subject)
	}
	// expires_at attribute must carry the Unix-seconds string used by DynamoDB TTL.
	exp, ok := item[expiresAtAttr].(*ddbtypes.AttributeValueMemberN)
	if !ok {
		t.Fatalf("expires_at attribute missing; item=%v", item)
	}
	if want := strconv.FormatInt(sess.ExpiresAt.Unix(), 10); exp.Value != want {
		t.Errorf("expires_at: got %q want %q", exp.Value, want)
	}
}

func TestSessionStore_PayloadBinaryBackend(t *testing.T) {
	// Confirm decodeSession also accepts a B (binary) payload — some
	// callers may prefer binary attributes for the JSON payload.
	s, backend := newTestStore()
	sess := mkSession("sid-1", "user-1")
	if err := s.Put(sess); err != nil {
		t.Fatal(err)
	}
	item := backend.items[sess.ID]
	if av, ok := item[payloadAttr].(*ddbtypes.AttributeValueMemberS); ok {
		item[payloadAttr] = &ddbtypes.AttributeValueMemberB{Value: []byte(av.Value)}
	}
	got, ok := s.Get(sess.ID)
	if !ok {
		t.Fatal("Get after binary re-encode: not found")
	}
	if got.Subject != sess.Subject {
		t.Errorf("subject: got %q want %q", got.Subject, sess.Subject)
	}
}

func TestSessionStore_GetSwallowsTransportError(t *testing.T) {
	backend := newMemDDB("sessions")
	backend.failOn = "GetItem"
	s := newSessionStoreWithBackend(backend, "sessions")
	if _, ok := s.Get("x"); ok {
		t.Fatal("Get: expected (nil,false) on transport error")
	}
}

func TestSessionStore_DeletePropagatesTransportError(t *testing.T) {
	backend := newMemDDB("sessions")
	backend.failOn = "DeleteItem"
	s := newSessionStoreWithBackend(backend, "sessions")
	if err := s.Delete("x"); err == nil {
		t.Fatal("Delete: expected error to propagate")
	}
}

// contains is a tiny helper to keep the test-only imports clean.
func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Compile-time assertion: *DynamoDBSessionStore satisfies auth.SessionStore.
var _ auth.SessionStore = (*DynamoDBSessionStore)(nil)
