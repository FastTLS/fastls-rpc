package main

import (
	"context"
	"strings"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/audit"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

type auditRPCStats struct{ sink *audit.Sink }
type auditRPCKey struct{}
type auditRPCStopKey struct{}

func (s auditRPCStats) begin(ctx context.Context, method string) (context.Context, *audit.Operation) {
	if op, _ := ctx.Value(auditRPCKey{}).(*audit.Operation); op != nil {
		return ctx, op
	}
	ctx, op := s.sink.Begin(ctx, "grpc", "request", "HTTP/2.0")
	if op == nil {
		return ctx, nil
	}
	method = strings.TrimPrefix(method, "/")
	unknown := method != pb.FastlsService_ServiceDesc.ServiceName+"/Fetch" && method != pb.FastlsService_ServiceDesc.ServiceName+"/Health"
	if unknown {
		audit.Result(ctx, "", 0, "not_found", "")
		audit.RPCStatus(ctx, "Unimplemented")
		stop := context.AfterFunc(ctx, func() { op.Done(ctx, 0, "") })
		ctx = context.WithValue(ctx, auditRPCStopKey{}, stop)
	}
	return context.WithValue(ctx, auditRPCKey{}, op), op
}
func (s auditRPCStats) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	ctx, _ = s.begin(ctx, info.FullMethodName)
	return ctx
}
func (auditRPCStats) HandleRPC(ctx context.Context, event stats.RPCStats) {
	end, ok := event.(*stats.End)
	if !ok {
		return
	}
	code := status.Code(end.Error)
	if end.Error != nil {
		classification := "transport_error"
		switch code {
		case codes.Canceled:
			classification = "cancelled"
		case codes.DeadlineExceeded:
			classification = "timeout"
		case codes.InvalidArgument:
			classification = "invalid_request"
		case codes.ResourceExhausted:
			classification = "request_too_large"
		case codes.Unimplemented:
			classification = "not_found"
		}
		phase := ""
		if classification == "timeout" {
			phase = "total"
		}
		audit.Fail(ctx, classification, phase)
	}
	if op, _ := ctx.Value(auditRPCKey{}).(*audit.Operation); op != nil {
		op.Done(ctx, 0, code.String())
	}
}
func (auditRPCStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (auditRPCStats) HandleConn(context.Context, stats.ConnStats)                       {}

func stopAuditFallback(ctx context.Context) {
	if stop, _ := ctx.Value(auditRPCStopKey{}).(func() bool); stop != nil {
		stop()
	}
}
