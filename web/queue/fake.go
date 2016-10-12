package queue

import (
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/aws/aws-sdk-go/service/sqs/sqsiface"
)

type Fake struct {
	sqsiface.SQSAPI

	SendMessageInput  *sqs.SendMessageInput
	SendMessageOutput *sqs.SendMessageOutput
	SendMessageError  error
}

func (f *Fake) SendMessage(msg *sqs.SendMessageInput) (*sqs.SendMessageOutput, error) {
	f.SendMessageInput = msg
	return f.SendMessageOutput, f.SendMessageError
}
