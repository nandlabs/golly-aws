// Package sns implements the golly messaging Provider interface for AWS SNS.
//
// SNS is a publish-only service. The Receive, ReceiveBatch, and AddListener
// methods return an unsupported operation error. For receiving messages
// published via SNS, use the SNS→SQS fan-out pattern with the sqs package.
//
// Import this package with a blank identifier to auto-register the SNS provider:
//
//	import _ "oss.nandlabs.io/golly-aws/sns"
package sns
