package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"

	fastls "github.com/FastTLS/fastls"
	"github.com/FastTLS/fastls/imitate"
	"github.com/gin-gonic/gin"
)

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
	URL                 string            `json:"url" binding:"required"`
	Method              string            `json:"method"`
	Headers             map[string]string `json:"headers"`
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
	OK      bool              `json:"ok"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Error   string            `json:"error,omitempty"`
}

// RPCServer RPC服务器
type RPCServer struct {
	client fastls.Fastls
}

// NewRPCServer 创建新的RPC服务器
func NewRPCServer() *RPCServer {
	return &RPCServer{
		client: fastls.NewClient(),
	}
}

// handleRPC 处理RPC请求
func (s *RPCServer) handleRPC(c *gin.Context) {
	var req JSONRPCRequest

	if err := c.ShouldBindJSON(&req); err != nil {
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
		result, rpcErr = s.handleFetch(req.Params)
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
func (s *RPCServer) handleFetch(params json.RawMessage) (interface{}, *RPCError) {
	var p FetchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    err.Error(),
		}
	}

	// 设置默认值
	if p.Method == "" {
		p.Method = "GET"
	}
	if p.Timeout == 0 {
		p.Timeout = 30
	}
	if p.Headers == nil {
		p.Headers = make(map[string]string)
	}

	// 构建Fastls选项
	options := fastls.Options{
		Headers:         p.Headers,
		Body:            p.Body,
		Proxy:           p.Proxy,
		Timeout:         p.Timeout,
		DisableRedirect: p.DisableRedirect,
		UserAgent:       p.UserAgent,
		Cookies:         p.Cookies,
	}

	// 优先使用参数中的代理信息
	if p.ParentProxy != "" {
		options.Proxy = p.ParentProxy
	} else if p.Proxy != "" {
		options.Proxy = p.Proxy
	}

	// 构建指纹配置
	requestConfig := &RequestFingerprintConfig{}

	// 从参数中获取配置
	if p.Browser != "" {
		requestConfig.Browser = p.Browser
		requestConfig.Override = true
	}
	if p.Fingerprint != nil {
		fpType, hasType := p.Fingerprint["type"]
		fpValue, hasValue := p.Fingerprint["value"]
		if hasType && hasValue {
			switch fpType {
			case "ja3":
				requestConfig.JA3 = fpValue
				requestConfig.Override = true
			case "ja4", "ja4r":
				requestConfig.JA4R = fpValue
				requestConfig.Override = true
			}
		}
	}
	if p.HTTP2SettingsString != "" {
		requestConfig.HTTP2SettingsString = p.HTTP2SettingsString
		requestConfig.Override = true
	}

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
		// 如果既没有指定指纹也没有指定浏览器，使用默认Firefox指纹
		if options.Fingerprint == nil || options.Fingerprint.IsEmpty() {
			imitate.Firefox(&options)
		}
	}

	// 执行请求
	resp, err := s.client.Do(p.URL, options, p.Method)

	// 处理错误
	if err != nil {
		status := 0
		if resp.Body != nil {
			resp.Body.Close()
		}
		return FetchResult{
			OK:     false,
			Status: status,
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
		return FetchResult{
			OK:     false,
			Status: resp.Status,
			Error:  "读取响应体失败: " + err.Error(),
		}, nil
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

	return FetchResult{
		OK:      true,
		Status:  resp.Status,
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
	flag.StringVar(&addr, "addr", ":8801", "监听地址 (例如: :8801 或 0.0.0.0:8801)")
	flag.StringVar(&addr, "a", ":8801", "监听地址 (addr 的缩写)")

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
	rpcServer := NewRPCServer()

	// 创建gin路由
	if debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// 添加CORS中间件
	r.Use(rpcCorsMiddleware())

	// 健康检查端点
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// RPC端点
	r.POST("/rpc", rpcServer.handleRPC)

	// 启动服务器
	log.Printf("RPC服务启动在端口 %s", addr)
	log.Printf("RPC端点: http://localhost%s/rpc", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}

// rpcCorsMiddleware CORS中间件
func rpcCorsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
