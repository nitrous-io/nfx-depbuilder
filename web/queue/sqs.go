package queue

import (
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/aws/aws-sdk-go/service/sqs/sqsiface"
)

func New() (sqsiface.SQSAPI, error) {
	sess, err := session.NewSession()
	if err != nil {
		return nil, err
	}

	return sqs.New(sess), nil
}
