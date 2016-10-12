package logservice

import (
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs/cloudwatchlogsiface"
)

type Fake struct {
	cloudwatchlogsiface.CloudWatchLogsAPI

	GetLogEventsInput  *cloudwatchlogs.GetLogEventsInput
	GetLogEventsOutput *cloudwatchlogs.GetLogEventsOutput
	GetLogEventsError  error
}

func (f *Fake) GetLogEvents(input *cloudwatchlogs.GetLogEventsInput) (*cloudwatchlogs.GetLogEventsOutput, error) {
	f.GetLogEventsInput = input
	return f.GetLogEventsOutput, f.GetLogEventsError
}
