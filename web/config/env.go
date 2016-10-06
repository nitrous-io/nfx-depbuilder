package config

import (
	"errors"
	"os"
)

var (
	S3Region = os.Getenv("NFX_WEB_S3_REGION")
	S3Bucket = os.Getenv("NFX_WEB_S3_BUCKET")
)

func Validate() error {
	if S3Region == "" {
		return errors.New("NFX_WEB_S3_REGION env var is required")
	}
	if S3Bucket == "" {
		return errors.New("NFX_WEB_S3_BUCKET env var is required")
	}

	return nil
}
