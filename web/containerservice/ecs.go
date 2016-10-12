package containerservice

import (
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/ecs/ecsiface"
)

func New() (ecsiface.ECSAPI, error) {
	sess, err := session.NewSession()
	if err != nil {
		return nil, err
	}

	return ecs.New(sess), nil
}
