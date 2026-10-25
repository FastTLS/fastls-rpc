package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"runtime"
	"time"

	fastls "github.com/FastTLS/fastls"
	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls-rpc/internal/fetchservice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GRPCServer gRPC服务器
type GRPCServer struct {
	pb.UnimplementedFastlsServiceServer
	client fastls.Fastls
}

// NewGRPCServer 创建新的gRPC服务器
func NewGRPCServer() *GRPCServer {
	return &GRPCServer{
		client: fastls.NewClient(),
	}
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
	result, err := fetchservice.Fetch(ctx, s.client, fetchservice.Params{
		URL: req.Url, Method: req.Method, Headers: headers, Body: req.Body,
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
	return &pb.FetchResponse{
		Ok: result.OK, Status: int32(result.Status), Headers: result.Headers,
		Body: result.Body, Error: result.Error,
	}, nil
}

func main() {
	var (
		addr        string
		debug       bool
		showVersion bool
		showHelp    bool
	)

	// 定义完整参数和缩写参数
	flag.StringVar(&addr, "addr", ":8802", "监听地址 (例如: :8802 或 0.0.0.0:8802)")
	flag.StringVar(&addr, "a", ":8802", "监听地址 (addr 的缩写)")

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
	grpcServer := NewGRPCServer()

	// 创建gRPC服务
	s := grpc.NewServer()

	// 注册服务
	pb.RegisterFastlsServiceServer(s, grpcServer)

	// 监听端口
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("监听端口失败: %v", err)
	}

	log.Printf("gRPC服务启动在端口 %s", addr)
	log.Printf("gRPC端点: localhost%s", addr)

	// 启动服务器
	if err := s.Serve(lis); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}
