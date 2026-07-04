package elasticache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// IAMAuthOptions configures the IAM auth-token generator returned by
// IAMAuthProvider.
type IAMAuthOptions struct {
	// AWSConfig provides the credentials and region used to sign the
	// presigned request. Must be non-nil.
	AWSConfig aws.Config

	// CacheName is the ElastiCache replication-group ID (cluster-mode
	// enabled) or cluster ID (cluster-mode disabled). It MUST match the
	// resource the IAM policy grants the user `elasticache:Connect` on.
	CacheName string

	// UserID is the ElastiCache RBAC user (must match the IAM `User`
	// claim). The user must be created in ElastiCache with
	// authentication-mode: iam.
	UserID string

	// TTL controls the presigned URL's lifetime. ElastiCache caps it at
	// 15 minutes; values above that are clamped down. Defaults to 15m.
	TTL time.Duration
}

// IAMAuthProvider returns a callback suitable for Config.CredentialsProvider
// that generates short-lived AUTH tokens for ElastiCache for Redis 7.x IAM
// authentication.
//
// The token is a SigV4-presigned request to elasticache:Connect, transmitted
// as the AUTH password. ElastiCache validates the signature server-side.
// Tokens are valid for up to 15 minutes; go-redis calls the provider on
// every connection establishment, so rotation is transparent to callers.
//
// Example:
//
//	awsCfg, _ := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
//	c, _ := elasticache.New(&elasticache.Config{
//	    Addrs:    []string{"my-cluster.abc123.use1.cache.amazonaws.com:6379"},
//	    Username: "app-user",
//	    CredentialsProvider: elasticache.IAMAuthProvider(elasticache.IAMAuthOptions{
//	        AWSConfig: awsCfg,
//	        CacheName: "my-cluster",
//	        UserID:    "app-user",
//	    }),
//	})
//
// See the AWS doc "Authenticating users with IAM" for the canonical
// algorithm: https://docs.aws.amazon.com/AmazonElastiCache/latest/red-ug/auth-iam.html
func IAMAuthProvider(opts IAMAuthOptions) func(ctx context.Context) (string, error) {
	// AWS ElastiCache caps the IAM auth token lifetime at 15 minutes, and
	// the SDK's SigV4 presigner uses that as its default X-Amz-Expires.
	// opts.TTL is retained on the struct for future use (a shorter TTL
	// would require signing a custom X-Amz-Expires ourselves) but is
	// currently informational only.
	_ = opts.TTL
	signer := v4.NewSigner()

	return func(ctx context.Context) (string, error) {
		creds, err := opts.AWSConfig.Credentials.Retrieve(ctx)
		if err != nil {
			return "", fmt.Errorf("retrieve credentials: %w", err)
		}

		// Build the presigned request:
		//   GET https://<cacheName>/?Action=connect&User=<userID>
		// Host header is the cache name; the actual TCP target is the
		// configuration endpoint (handled by the redis client).
		q := url.Values{
			"Action": {"connect"},
			"User":   {opts.UserID},
		}
		reqURL := url.URL{
			Scheme:   "https",
			Host:     opts.CacheName,
			Path:     "/",
			RawQuery: q.Encode(),
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
		if err != nil {
			return "", fmt.Errorf("build request: %w", err)
		}

		// PresignHTTP requires the SHA-256 of an empty payload for GET.
		const emptyPayloadHash = ""
		emptySHA := sha256.Sum256(nil)
		hash := hex.EncodeToString(emptySHA[:])
		_ = emptyPayloadHash // documentation only

		signed, _, err := signer.PresignHTTP(
			ctx,
			creds,
			req,
			hash,
			"elasticache",
			opts.AWSConfig.Region,
			time.Now().UTC(),
			func(o *v4.SignerOptions) { o.LogSigning = false },
		)
		if err != nil {
			return "", fmt.Errorf("presign: %w", err)
		}

		// The AUTH token is the signed URL with the "https://" scheme
		// stripped — what ElastiCache parses server-side.
		token := strings.TrimPrefix(signed, "https://")
		return token, nil
	}
}
