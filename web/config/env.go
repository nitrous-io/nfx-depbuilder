package config

import (
	"errors"
	"os"
)

var (
	AWSRegion   = os.Getenv("AWS_REGION")
	S3Bucket    = os.Getenv("NFX_WEB_S3_BUCKET")
	SQSQueueURL = os.Getenv("NFX_WEB_SQS_QUEUE_URL")
)

func Validate() error {
	if AWSRegion == "" {
		return errors.New("AWS_REGION env var is required")
	}
	if S3Bucket == "" {
		return errors.New("NFX_WEB_S3_BUCKET env var is required")
	}
	if SQSQueueURL == "" {
		return errors.New("NFX_WEB_SQS_QUEUE_URL env var is required")
	}

	return nil
}
