package interceptor

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sync"

	"google.golang.org/grpc"
)

var SoftBypassNewReq bool

const (
	// as "/runtime.v1alpha2.RuntimeService/StopPodSandbox"
	CriDistinctString = `^/runtime\.(.+)\.RuntimeService`
)

var reg *regexp.Regexp

func init() {
	reg = regexp.MustCompile(CriDistinctString)
}

// RequestCountDecider is a user-provided function for deciding whether count a request is flying.
type RequestCountDecider func(ctx context.Context, fullMethodName string, servingObject interface{}) bool

// FlyingRequestCountInterceptor returns a new unary server interceptors that counts the flying requests.
func FlyingRequestCountInterceptor(decider RequestCountDecider, wq *sync.WaitGroup) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if SoftBypassNewReq && findoutCriRequest(info.FullMethod) {
			return nil, fmt.Errorf("service entering lame duck status,all request will bypass")
		}
		if decider(ctx, info.FullMethod, info.Server) {
			wq.Add(1)
			defer wq.Done()
		}
		resp, err := handler(ctx, req)
		return resp, err
	}
}

func FlyingReqCountDecider(ctx context.Context, fullMethodName string, servingObject interface{}) bool {
	methodName := path.Base(fullMethodName)
	switch methodName {
	case
		"RunPodSandbox",
		"StartPodSandbox",
		"StopPodSandbox",
		"RemovePodSandbox",
		"CreateContainer",
		"StartContainer",
		"StopContainer",
		"RemoveContainer",
		"PauseContainer",
		"UnpauseContainer":
		return true
	default:
		return false
	}
}
func findoutCriRequest(fullMethodName string) bool {
	return reg.MatchString(fullMethodName)
}
