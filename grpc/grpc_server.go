package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	fastls "github.com/FastTLS/fastls"
	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/fetchservice"
	"github.com/FastTLS/fastls-rpc/internal/serverlifecycle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GRPCServer gRPC服务器
type GRPCServer struct {
	pb.UnimplementedFastlsServiceServer
	executor *fetchservice.Executor
}

// NewGRPCServer 创建新的gRPC服务器
func NewGRPCServer() *GRPCServer {
	s, _ := NewGRPCServerWithLimits(fetchservice.Limits{})
	return s
}

func NewGRPCServerWithLimits(limits fetchservice.Limits) (*GRPCServer, error) {
	return NewGRPCServerWithConfig(fetchservice.Config{Limits: limits})
}

func NewGRPCServerWithConfig(config fetchservice.Config) (*GRPCServer, error) {
	executor, err := fetchservice.NewExecutorWithConfig(config)
	if err != nil {
		return nil, err
	}
	return &GRPCServer{executor: executor}, nil
}

// newTransportServer applies the same validated startup budget to the wire server.
func (s *GRPCServer) newTransportServer() *grpc.Server {
	server := grpc.NewServer(grpc.MaxRecvMsgSize(int(s.executor.RequestMaximum())))
	pb.RegisterFastlsServiceServer(server, s)
	return server
}

// Health 健康检查
func (s *GRPCServer) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{
		Status: "ok",
	}, nil
}

// Fetch 发送HTTP请求
func (s *GRPCServer) Fetch(ctx context.Context, req *pb.FetchRequest) (*pb.FetchResponse, error) {
	// 构建请求头
	headers := make(map[string]string)
	for k, v := range req.Headers {
		headers[k] = v
	}
	trailers := make(http.Header, len(req.Trailers))
	for key, values := range req.Trailers {
		if values != nil {
			trailers[key] = append([]string(nil), values.Values...)
		}
	}

	// 构建Cookie列表
	var cookies []fastls.Cookie
	for _, cookie := range req.Cookies {
		var expires time.Time
		if cookie.Expires > 0 {
			expires = time.Unix(cookie.Expires, 0)
		}
		cookies = append(cookies, fastls.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Path:     cookie.Path,
			Domain:   cookie.Domain,
			Expires:  expires,
			HTTPOnly: cookie.HttpOnly,
			Secure:   cookie.Secure,
		})
	}

	fingerprintType, fingerprintValue := "", ""
	if req.Fingerprint != nil {
		fingerprintType = req.Fingerprint.Type
		fingerprintValue = req.Fingerprint.Value
	}
	result, err := s.executor.Fetch(ctx, fetchservice.Params{
		IncludeBodyBase64: req.IncludeBodyBase64,
		URL:               req.Url, Method: req.Method, Headers: headers, Trailers: trailers, Body: req.Body,
		Proxy: req.Proxy, ParentProxy: req.ParentProxy, Timeout: int(req.Timeout),
		DisableRedirect: req.DisableRedirect, UserAgent: req.UserAgent,
		FingerprintSet: req.Fingerprint != nil, FingerprintType: fingerprintType, FingerprintValue: fingerprintValue,
		Browser: req.Browser, Cookies: cookies,
		HTTP2SettingsString: req.Http2SettingsString,
	})
	if err != nil {
		if _, ok := err.(*fetchservice.ConfigError); ok {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	responseTrailers := make(map[string]*pb.HeaderValues, len(result.Trailers))
	for key, values := range result.Trailers {
		responseTrailers[key] = &pb.HeaderValues{Values: append([]string(nil), values...)}
	}
	headerValues := make(map[string]*pb.HeaderValues, len(result.HeaderValues))
	for key, values := range result.HeaderValues {
		headerValues[key] = &pb.HeaderValues{Values: append([]string(nil), values...)}
	}
	return &pb.FetchResponse{
		Ok: result.OK, Status: int32(result.Status), Protocol: result.Protocol, FinalUrl: result.FinalURL,
		Headers: result.Headers, Trailers: responseTrailers, Body: result.Body, Error: result.Error,
		HeaderValues: headerValues, BodyBase64: result.BodyBase64, ErrorCode: result.ErrorCode, TimeoutPhase: result.TimeoutPhase,
	}, nil
}

func main() {
	var (
		addr           string
		debug          bool
		showVersion    bool
		showHelp       bool
		maximum        int64
		requestMaximum int64
		inflight       int
		policyFile     string
	)

	// 定义完整参数和缩写参数
	flag.StringVar(&addr, "addr", ":8802", "监听地址 (例如: :8802 或 0.0.0.0:8802)")
	flag.StringVar(&addr, "a", ":8802", "监听地址 (addr 的缩写)")
	flag.Int64Var(&maximum, "max-response-bytes", 0, "Maximum wire and decoded response bytes; 0 is unlimited")
	flag.Int64Var(&requestMaximum, "max-request-bytes", 4<<20, "Maximum decompressed protobuf request bytes (1..64 MiB); 0 selects 4 MiB")
	flag.IntVar(&inflight, "max-inflight", 0, "Maximum active fetch calls; 0 is unlimited")
	flag.StringVar(&policyFile, "address-policy", "", "Trusted address policy JSON file; unsupported cores refuse startup")

	flag.BoolVar(&debug, "debug", false, "启用 Debug 模式，显示详细的调试日志")
	flag.BoolVar(&debug, "d", false, "启用 Debug 模式 (debug 的缩写)")

	flag.BoolVar(&showVersion, "version", false, "显示版本信息并退出")
	flag.BoolVar(&showVersion, "v", false, "显示版本信息并退出 (version 的缩写)")

	flag.BoolVar(&showHelp, "help", false, "显示帮助信息并退出")
	flag.BoolVar(&showHelp, "h", false, "显示帮助信息并退出 (help 的缩写)")

	flag.Usage = func() {
		printHelp()
	}

	flag.Parse()

	if showHelp || len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		printHelp()
		os.Exit(0)
	}

	if showVersion {
		printVersion()
		os.Exit(0)
	}

	// 设置最大CPU核心数
	runtime.GOMAXPROCS(runtime.NumCPU())

	// 创建gRPC服务器
	grpcServer, err := NewGRPCServerWithConfig(fetchservice.Config{Limits: fetchservice.Limits{MaxRequestBytes: requestMaximum, MaxResponseBytes: maximum, MaxInflight: inflight}, AddressPolicyFile: policyFile})
	if err != nil {
		log.Fatal(err)
	}

	// 创建gRPC服务
	s := grpcServer.newTransportServer()

	// 监听端口
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("监听端口失败: %v", err)
	}

	log.Printf("gRPC服务启动在端口 %s", addr)
	log.Printf("gRPC端点: %s", lis.Addr())

	// 启动服务器
	shutdownContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if err := serverlifecycle.ServeGRPC(shutdownContext, lis, s); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
	if shutdownContext.Err() != nil {
		log.Printf("gRPC服务已安全停止")
	}
}
