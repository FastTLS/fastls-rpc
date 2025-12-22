package main

import (
	"context"
	"io"
	"log"
	"net"
	"runtime"
	"time"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls/imitate"
	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
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

	// 处理指纹
	if req.Fingerprint != nil {
		switch req.Fingerprint.Type {
		case "ja3":
			options.Fingerprint = fastls.Ja3Fingerprint{
				FingerprintValue: req.Fingerprint.Value,
			}
		case "ja4", "ja4r":
			options.Fingerprint = fastls.Ja4Fingerprint{
				FingerprintValue: req.Fingerprint.Value,
			}
		}
	}

	// 如果没有指定指纹，根据浏览器类型设置指纹
	if (options.Fingerprint == nil || options.Fingerprint.IsEmpty()) && req.Browser != "" {
		switch req.Browser {
		case "chrome":
			imitate.Chrome(&options)
		case "chrome120":
			imitate.Chrome120(&options)
		case "chrome142":
			imitate.Chrome142(&options)
		case "chromium":
			imitate.Chromium(&options)
		case "edge":
			imitate.Edge(&options)
		case "firefox":
			imitate.Firefox(&options)
		case "safari":
			imitate.Safari(&options)
		case "opera":
			imitate.Opera(&options)
		default:
			imitate.Firefox(&options)
		}
	}

	// 如果既没有指定指纹也没有指定浏览器，使用默认Firefox指纹
	if options.Fingerprint == nil || options.Fingerprint.IsEmpty() {
		imitate.Firefox(&options)
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
	// 设置最大CPU核心数
	runtime.GOMAXPROCS(runtime.NumCPU())

	// 创建gRPC服务器
	grpcServer := NewGRPCServer()

	// 创建gRPC服务
	s := grpc.NewServer()

	// 注册服务
	pb.RegisterFastlsServiceServer(s, grpcServer)

	// 监听端口
	port := ":8802"
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("监听端口失败: %v", err)
	}

	log.Printf("gRPC服务启动在端口 %s", port)
	log.Printf("gRPC端点: localhost%s", port)

	// 启动服务器
	if err := s.Serve(lis); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}
