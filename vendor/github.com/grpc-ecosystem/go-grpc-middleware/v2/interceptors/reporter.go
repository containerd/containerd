// Copyright (c) The go-grpc-middleware Authors.
// Licensed under the Apache License 2.0.

package interceptors

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
)

type GRPCType string

// Timer is a helper interface to time functions.
// Useful for interceptors to record the total
// time elapsed since completion of a call.
type Timer interface {
	ObserveDuration() time.Duration
}

// zeroTimer.
type zeroTimer struct {
}

func (zeroTimer) ObserveDuration() time.Duration {
	return 0
}

var EmptyTimer = &zeroTimer{}

const (
	Unary        GRPCType = "unary"
	ClientStream GRPCType = "client_stream"
	ServerStream GRPCType = "server_stream"
	BidiStream   GRPCType = "bidi_stream"
)

var (
	AllCodes = []codes.Code{
		codes.OK, codes.Canceled, codes.Unknown, codes.InvalidArgument, codes.DeadlineExceeded, codes.NotFound,
		codes.AlreadyExists, codes.PermissionDenied, codes.Unauthenticated, codes.ResourceExhausted,
		codes.FailedPrecondition, codes.Aborted, codes.OutOfRange, codes.Unimplemented, codes.Internal,
		codes.Unavailable, codes.DataLoss,
	}
)

func SplitMethodName(fullMethod string) (string, string) {
	fullMethod = strings.TrimPrefix(fullMethod, "/") // remove leading slash
	if i := strings.Index(fullMethod, "/"); i >= 0 {
		return fullMethod[:i], fullMethod[i+1:]
	}
	return "unknown", "unknown"
}

type CallMeta struct {
	ReqProtoOrNil interface{}
	Typ           GRPCType
	Service       string
	Method        string
}

func (c CallMeta) FullMethod() string {
	return fmt.Sprintf("/%s/%s", c.Service, c.Method)
}

type ClientReportable interface {
	ClientReporter(context.Context, CallMeta) (Reporter, context.Context)
}

type ServerReportable interface {
	ServerReporter(context.Context, CallMeta) (Reporter, context.Context)
}

// CommonReportableFunc helper allows an easy way to implement reporter with common client and server logic.
type CommonReportableFunc func(ctx context.Context, c CallMeta, isClient bool) (Reporter, context.Context)

func (f CommonReportableFunc) ClientReporter(ctx context.Context, c CallMeta) (Reporter, context.Context) {
	return f(ctx, c, true)
}

func (f CommonReportableFunc) ServerReporter(ctx context.Context, c CallMeta) (Reporter, context.Context) {
	return f(ctx, c, false)
}

type Reporter interface {
	PostCall(err error, rpcDuration time.Duration)
	PostMsgSend(reqProto interface{}, err error, sendDuration time.Duration)
	PostMsgReceive(replyProto interface{}, err error, recvDuration time.Duration)
}

var _ Reporter = NoopReporter{}

type NoopReporter struct{}

func (NoopReporter) PostCall(error, time.Duration)                    {}
func (NoopReporter) PostMsgSend(interface{}, error, time.Duration)    {}
func (NoopReporter) PostMsgReceive(interface{}, error, time.Duration) {}

type report struct {
	rpcType   GRPCType
	service   string
	method    string
	startTime time.Time
}

func newReport(typ GRPCType, fullMethod string) report {
	r := report{
		startTime: time.Now(),
		rpcType:   typ,
	}
	r.service, r.method = SplitMethodName(fullMethod)
	return r
}
