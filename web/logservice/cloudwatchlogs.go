package logservice

import (
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs/cloudwatchlogsiface"
)

func New() (cloudwatchlogsiface.CloudWatchLogsAPI, error) {
	sess, err := session.NewSession()
	if err != nil {
		return nil, err
	}

	return cloudwatchlogs.New(sess), nil
}
