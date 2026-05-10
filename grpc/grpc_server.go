package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"time"

	fastls "github.com/FastTLS/fastls"
	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"github.com/FastTLS/fastls/imitate"
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
	// 设置默认值
	method := req.Method
	if method == "" {
		method = "GET"
	}
	timeout := int(req.Timeout)
	if timeout == 0 {
		timeout = 30
	}

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

	// 构建Fastls选项
	options := fastls.Options{
		Headers:         headers,
		Body:            req.Body,
		Proxy:           req.Proxy,
		Timeout:         timeout,
		DisableRedirect: req.DisableRedirect,
		UserAgent:       req.UserAgent,
		Cookies:         cookies,
	}

	// 优先使用参数中的代理信息
	// 注意：proto 文件已更新，需要重新生成 Go 代码才能使用新字段
	// 生成命令: cd grpc && protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/fastls.proto
	if req.Proxy != "" {
		options.Proxy = req.Proxy
	}
	// TODO: 重新生成 proto 后启用
	// if req.ParentProxy != "" {
	// 	options.Proxy = req.ParentProxy
	// }

	// 构建指纹配置（优先级：参数中的配置 > 请求体中的配置）
	requestConfig := &RequestFingerprintConfig{}

	// 优先使用参数中的配置
	if req.Browser != "" {
		requestConfig.Browser = req.Browser
		requestConfig.Override = true
	}
	if req.Fingerprint != nil {
		switch req.Fingerprint.Type {
		case "ja3":
			requestConfig.JA3 = req.Fingerprint.Value
			requestConfig.Override = true
		case "ja4", "ja4r":
			requestConfig.JA4R = req.Fingerprint.Value
			requestConfig.Override = true
		}
	}
	// TODO: 重新生成 proto 后启用
	// if req.Http2SettingsString != "" {
	// 	requestConfig.HTTP2SettingsString = req.Http2SettingsString
	// 	requestConfig.Override = true
	// }

	// 应用指纹配置
	if requestConfig.Override {
		applyImitateConfig(&options, requestConfig)

		// 优先使用参数中的 HTTP2SettingsString
		if requestConfig.HTTP2SettingsString != "" {
			options.HTTP2SettingsString = requestConfig.HTTP2SettingsString
		}

		// 如果 HTTP2SettingsString 不为空，则解析并覆盖 HTTP2Settings 和 PHeaderOrderKeys
		if options.HTTP2SettingsString != "" {
			h2Settings, pHeaderOrderKeys, err := fastls.ParseH2SettingsStringWithPHeaderOrder(options.HTTP2SettingsString)
			if err != nil {
				log.Printf("解析 HTTP2SettingsString 失败: %v", err)
			} else {
				// 覆盖 HTTP2Settings
				options.HTTP2Settings = fastls.ToHTTP2Settings(h2Settings)
				// 如果解析出 PHeaderOrderKeys，则覆盖
				if len(pHeaderOrderKeys) > 0 {
					options.PHeaderOrderKeys = pHeaderOrderKeys
				}
			}
		}
	} else {
		// 如果没有参数配置，使用默认逻辑
		// 如果既没有指定指纹也没有指定浏览器，使用默认Firefox指纹
		if options.Fingerprint == nil || options.Fingerprint.IsEmpty() {
			imitate.Firefox(&options)
		}
	}

	// 执行请求
	resp, err := s.client.Do(req.Url, options, method)

	// 处理错误
	if err != nil {
		return &pb.FetchResponse{
			Ok:     false,
			Status: 0,
			Error:  err.Error(),
		}, nil
	}

	// 确保响应体被关闭
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	// 读取响应体
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "读取响应体失败: %v", err)
	}

	// 解码响应体
	contentEncoding := resp.Headers["Content-Encoding"]
	var decodedBody string
	if contentEncoding != "" {
		decodedBody = fastls.DecompressBody(bodyBytes, []string{contentEncoding}, nil)
	} else {
		contentType := resp.Headers["Content-Type"]
		if contentType != "" {
			decodedBody = fastls.DecompressBody(bodyBytes, nil, []string{contentType})
		} else {
			decodedBody = string(bodyBytes)
		}
	}

	// 构建响应
	return &pb.FetchResponse{
		Ok:      true,
		Status:  int32(resp.Status),
		Headers: resp.Headers,
		Body:    decodedBody,
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
