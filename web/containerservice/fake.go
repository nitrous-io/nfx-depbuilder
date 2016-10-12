package containerservice

import (
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/ecs/ecsiface"
)

type Fake struct {
	ecsiface.ECSAPI

	RunTaskInput  *ecs.RunTaskInput
	RunTaskOutput *ecs.RunTaskOutput
	RunTaskError  error
}

func (f *Fake) RunTask(input *ecs.RunTaskInput) (*ecs.RunTaskOutput, error) {
	f.RunTaskInput = input
	return f.RunTaskOutput, f.RunTaskError
}
