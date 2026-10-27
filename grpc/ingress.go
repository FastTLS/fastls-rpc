package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/FastTLS/fastls-rpc/internal/audit"
	"strings"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/ingress"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"
)

type grpcIngressConfig struct {
	ingress.Limits
	MaxStreamsPerConnection int
	ConnectionTimeout       time.Duration
	RPCTimeout              time.Duration
}

func (c *grpcIngressConfig) bindFlags(flags *flag.FlagSet) {
	c.Limits.BindFlags(flags)
	flags.IntVar(&c.MaxStreamsPerConnection, "max-streams-per-connection", 0, "Maximum HTTP/2 streams per connection; 0 keeps gRPC default")
	flags.DurationVar(&c.ConnectionTimeout, "connection-timeout", 0, "HTTP/2 connection handshake deadline; 0 keeps gRPC 120s default")
	flags.DurationVar(&c.RPCTimeout, "rpc-timeout", 0, "Maximum admitted RPC lifetime, including message reads; 0 adds no server deadline")
}

func (c grpcIngressConfig) validate() error {
	if err := c.Limits.Validate(); err != nil {
		return err
	}
	if c.MaxStreamsPerConnection < 0 || c.MaxStreamsPerConnection > 65536 {
		return fmt.Errorf("max-streams-per-connection must be 0..65536")
	}
	if err := ingress.ValidateDuration("connection-timeout", c.ConnectionTimeout); err != nil {
		return err
	}
	return ingress.ValidateDuration("rpc-timeout", c.RPCTimeout)
}

func (s *GRPCServer) newTransportServerWithIngress(config grpcIngressConfig) (*grpc.Server, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	gate, err := ingress.NewGate(config.MaxAdmittedRequests)
	if err != nil {
		return nil, err
	}
	var options []grpc.ServerOption
	if config.MaxHeaderBytes > 0 {
		options = append(options, grpc.MaxHeaderListSize(uint32(config.MaxHeaderBytes)))
	}
	if config.MaxStreamsPerConnection > 0 {
		options = append(options, grpc.MaxConcurrentStreams(uint32(config.MaxStreamsPerConnection)))
	}
	if config.ConnectionTimeout > 0 {
		options = append(options, grpc.ConnectionTimeout(config.ConnectionTimeout))
	}
	admissionEnabled := config.MaxAdmittedRequests > 0 || config.RPCTimeout > 0
	if admissionEnabled || s.audit != nil {
		if admissionEnabled {
			options = append(options, grpc.UnaryInterceptor(func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				defer releaseAdmission(ctx)
				response, err := handler(ctx, request)
				if ctx.Err() != nil {
					return nil, status.FromContextError(ctx.Err()).Err()
				}
				return response, err
			}), grpc.StatsHandler(admissionStats{}))
		}
		options = append(options, grpc.InTapHandle(func(ctx context.Context, info *tap.Info) (context.Context, error) {
			ctx, operation := (auditRPCStats{sink: s.audit}).begin(ctx, info.FullMethodName)
			original := ctx
			release, err := gate.Acquire(ctx)
			if err != nil {
				stopAuditFallback(ctx)
				if ctx.Err() != nil {
					operation.Done(ctx, 0, status.Code(status.FromContextError(ctx.Err()).Err()).String())
					return ctx, status.FromContextError(ctx.Err()).Err()
				}
				audit.Result(ctx, "", 0, "overloaded", "")
				audit.RPCStatus(ctx, "ResourceExhausted")
				operation.Done(ctx, 0, "ResourceExhausted")
				return ctx, status.Error(codes.ResourceExhausted, "inbound request limit reached")
			}
			cancel := func() {}
			if config.RPCTimeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, config.RPCTimeout)
			}
			// A reset can cancel the transport context while a decoder is still
			// running. Known unary methods must retain their permit until their
			// interceptor or stats.End runs, rather than release on cancellation.
			knownUnary := false
			method := strings.TrimPrefix(info.FullMethodName, "/")
			for _, descriptor := range pb.FastlsService_ServiceDesc.Methods {
				if method == pb.FastlsService_ServiceDesc.ServiceName+"/"+descriptor.MethodName {
					knownUnary = true
					break
				}
			}
			context.AfterFunc(original, func() {
				cancel()
				// Unknown methods never reach a unary handler or stats.End.
				if !knownUnary {
					release()
				}
			})
			return context.WithValue(ctx, admissionLeaseKey{}, release), nil
		}))
	}
	return s.transportServer(options...), nil
}

type admissionLeaseKey struct{}

func releaseAdmission(ctx context.Context) {
	if release, ok := ctx.Value(admissionLeaseKey{}).(func()); ok {
		release()
	}
}

type admissionStats struct{}

func (admissionStats) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }
func (admissionStats) HandleRPC(ctx context.Context, event stats.RPCStats) {
	if _, ok := event.(*stats.End); ok {
		releaseAdmission(ctx)
	}
}
func (admissionStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (admissionStats) HandleConn(context.Context, stats.ConnStats)                       {}
