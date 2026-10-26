package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls-rpc/internal/fetchservice"
	"github.com/FastTLS/fastls-rpc/internal/serverlifecycle"
	"github.com/gin-gonic/gin"
)

const maxRPCRequestBodySize = 4 << 20

// JSONRPCRequest JSON-RPC 2.0 请求结构
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc" binding:"required"`
	Method  string          `json:"method" binding:"required"`
	Params  json.RawMessage `json:"params"`
	ID      interface{}     `json:"id"`
}

// JSONRPCResponse JSON-RPC 2.0 响应结构
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// RPCError JSON-RPC 错误结构
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// FetchParams 请求参数
type FetchParams struct {
	IncludeBodyBase64   bool              `json:"include_body_base64"`
	URL                 string            `json:"url" binding:"required"`
	Method              string            `json:"method"`
	Headers             map[string]string `json:"headers"`
	Trailers            http.Header       `json:"trailers,omitempty"`
	Body                string            `json:"body"`
	Proxy               string            `json:"proxy"`
	Timeout             int               `json:"timeout"`
	DisableRedirect     bool              `json:"disableRedirect"`
	UserAgent           string            `json:"userAgent"`
	Fingerprint         map[string]string `json:"fingerprint"`
	Browser             string            `json:"browser"`
	Cookies             []fastls.Cookie   `json:"cookies"`
	HTTP2SettingsString string            `json:"http2_settings_string"`
	ParentProxy         string            `json:"parent_proxy"`
}

// FetchResult 请求结果
type FetchResult struct {
	OK           bool              `json:"ok"`
	Status       int               `json:"status"`
	Protocol     string            `json:"protocol,omitempty"`
	FinalURL     string            `json:"final_url,omitempty"`
	Headers      map[string]string `json:"headers"`
	Trailers     http.Header       `json:"trailers,omitempty"`
	Body         string            `json:"body"`
	Error        string            `json:"error,omitempty"`
	HeaderValues http.Header       `json:"header_values,omitempty"`
	BodyBase64   string            `json:"body_base64,omitempty"`
	ErrorCode    string            `json:"error_code,omitempty"`
	TimeoutPhase string            `json:"timeout_phase,omitempty"`
}

// RPCServer RPC服务器
type RPCServer struct {
	executor *fetchservice.Executor
}

// NewRPCServer 创建新的RPC服务器
func NewRPCServer() *RPCServer {
	s, _ := NewRPCServerWithLimits(fetchservice.Limits{})
	return s
}

func NewRPCServerWithLimits(limits fetchservice.Limits) (*RPCServer, error) {
	return NewRPCServerWithConfig(fetchservice.Config{Limits: limits})
}

func NewRPCServerWithConfig(config fetchservice.Config) (*RPCServer, error) {
	executor, err := fetchservice.NewExecutorWithConfig(config)
	if err != nil {
		return nil, err
	}
	return &RPCServer{executor: executor}, nil
}

// handleRPC 处理RPC请求
func (s *RPCServer) handleRPC(c *gin.Context) {
	var req JSONRPCRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			c.JSON(http.StatusRequestEntityTooLarge, JSONRPCResponse{
				JSONRPC: "2.0",
				Error: &RPCError{
					Code:    -32600,
					Message: "Invalid Request",
					Data:    fmt.Sprintf("request body exceeds %d byte limit", maxBytesError.Limit),
				},
				ID: nil,
			})
			return
		}
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: "2.0",
			Error: &RPCError{
				Code:    -32700,
				Message: "Parse error",
				Data:    err.Error(),
			},
			ID: nil,
		})
		return
	}

	// 验证JSON-RPC版本
	if req.JSONRPC != "2.0" {
		c.JSON(http.StatusOK, JSONRPCResponse{
			JSONRPC: "2.0",
			Error: &RPCError{
				Code:    -32600,
				Message: "Invalid Request",
				Data:    "jsonrpc must be '2.0'",
			},
			ID: req.ID,
		})
		return
	}

	// 根据方法名路由
	var result interface{}
	var rpcErr *RPCError

	switch req.Method {
	case "fetch":
		result, rpcErr = s.handleFetch(c.Request.Context(), req.Params)
	case "health":
		result = map[string]string{"status": "ok"}
	default:
		rpcErr = &RPCError{
			Code:    -32601,
			Message: "Method not found",
			Data:    "Unknown method: " + req.Method,
		}
	}

	// 构建响应
	response := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	if rpcErr != nil {
		response.Error = rpcErr
	} else {
		response.Result = result
	}

	c.JSON(http.StatusOK, response)
}

// handleFetch 处理fetch请求
func (s *RPCServer) handleFetch(ctx context.Context, params json.RawMessage) (interface{}, *RPCError) {
	var p FetchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    err.Error(),
		}
	}

	fingerprintType, fingerprintValue := "", ""
	if p.Fingerprint != nil {
		fingerprintType = p.Fingerprint["type"]
		fingerprintValue = p.Fingerprint["value"]
	}
	result, err := s.executor.Fetch(ctx, fetchservice.Params{
		IncludeBodyBase64: p.IncludeBodyBase64,
		URL:               p.URL, Method: p.Method, Headers: p.Headers, Trailers: p.Trailers, Body: p.Body,
		Proxy: p.Proxy, ParentProxy: p.ParentProxy, Timeout: p.Timeout,
		DisableRedirect: p.DisableRedirect, UserAgent: p.UserAgent,
		FingerprintSet: p.Fingerprint != nil, FingerprintType: fingerprintType, FingerprintValue: fingerprintValue,
		Browser: p.Browser, Cookies: p.Cookies,
		HTTP2SettingsString: p.HTTP2SettingsString,
	})
	if err != nil {
		if _, ok := err.(*fetchservice.ConfigError); ok {
			return nil, &RPCError{Code: -32602, Message: "Invalid params", Data: err.Error()}
		}
		return nil, &RPCError{Code: -32603, Message: "Internal error", Data: err.Error()}
	}
	return FetchResult(result), nil
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
	flag.StringVar(&addr, "addr", ":8801", "监听地址 (例如: :8801 或 0.0.0.0:8801)")
	flag.StringVar(&addr, "a", ":8801", "监听地址 (addr 的缩写)")
	flag.Int64Var(&maximum, "max-response-bytes", 0, "Maximum wire and decoded response bytes; 0 is unlimited")
	flag.Int64Var(&requestMaximum, "max-request-bytes", 4<<20, "Maximum complete JSON request bytes (1..64 MiB); 0 selects 4 MiB")
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

	// 创建RPC服务器
	rpcServer, err := NewRPCServerWithConfig(fetchservice.Config{Limits: fetchservice.Limits{MaxRequestBytes: requestMaximum, MaxResponseBytes: maximum, MaxInflight: inflight}, AddressPolicyFile: policyFile})
	if err != nil {
		log.Fatal(err)
	}

	// 创建gin路由
	if debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	r := newJSONRPCRouter(rpcServer)

	// 启动服务器
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("监听端口失败: %v", err)
	}
	shutdownContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	server := &http.Server{Handler: r, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("RPC服务启动在端口 %s", listener.Addr())
	log.Printf("RPC端点: http://%s/rpc", listener.Addr())
	if err := serverlifecycle.ServeHTTP(shutdownContext, listener, server); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
	if shutdownContext.Err() != nil {
		log.Printf("RPC服务已安全停止")
	}
}

func newJSONRPCRouter(rpcServer *RPCServer) *gin.Engine {
	r := gin.Default()
	r.Use(rpcCorsMiddleware())
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.POST("/rpc", rpcRequestBodyLimit(rpcServer.executor.RequestMaximum()), rpcServer.handleRPC)
	return r
}

func rpcRequestBodyLimit(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			body := http.MaxBytesReader(c.Writer, c.Request.Body, limit)
			data, err := io.ReadAll(body)
			body.Close()
			if err != nil {
				status, code, message := http.StatusOK, -32700, "Parse error"
				detail := err.Error()
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					status, code = http.StatusRequestEntityTooLarge, -32600
					message = "Invalid Request"
					detail = fmt.Sprintf("request body exceeds %d byte limit", tooLarge.Limit)
				}
				c.AbortWithStatusJSON(status, JSONRPCResponse{JSONRPC: "2.0", Error: &RPCError{Code: code, Message: message, Data: detail}})
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(data))
		}
		c.Next()
	}
}

// rpcCorsMiddleware CORS中间件
func rpcCorsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Add("Vary", "Origin")
		}
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.Writer.Header().Add("Vary", "Access-Control-Request-Method")
			c.Writer.Header().Add("Vary", "Access-Control-Request-Headers")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
