package config

import (
	"errors"
	"os"
)

var (
	AWSRegion         = os.Getenv("AWS_REGION")
	S3Bucket          = os.Getenv("NFX_WEB_S3_BUCKET")
	SQSQueueURL       = os.Getenv("NFX_WEB_SQS_QUEUE_URL")
	TaskDefinitionARN = os.Getenv("NFX_WEB_TASK_DEF_ARN")
	TaskClusterName   = os.Getenv("NFX_WEB_TASK_CLUSTER_NAME")
	ResponseLogGroup  = os.Getenv("NFX_WEB_RESPONSE_LOG_GROUP")
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
	if TaskDefinitionARN == "" {
		return errors.New("NFX_WEB_TASK_DEF_ARN env var is required")
	}
	if TaskClusterName == "" {
		return errors.New("NFX_WEB_TASK_CLUSTER_NAME env var is required")
	}
	if ResponseLogGroup == "" {
		return errors.New("NFX_WEB_RESPONSE_LOG_GROUP env var is required")
	}

	return nil
}
