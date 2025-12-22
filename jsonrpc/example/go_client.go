package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// JSONRPCRequest JSON-RPC 2.0 请求
type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
	ID      int         `json:"id"`
}

// JSONRPCResponse JSON-RPC 2.0 响应
type JSONRPCResponse struct {
	JSONRPC string                 `json:"jsonrpc"`
	Result  map[string]interface{} `json:"result,omitempty"`
	Error   *RPCError              `json:"error,omitempty"`
	ID      int                    `json:"id"`
}

// RPCError RPC错误
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// FetchParams 请求参数
type FetchParams struct {
	URL             string            `json:"url"`
	Method          string            `json:"method,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	Proxy           string            `json:"proxy,omitempty"`
	Timeout         int               `json:"timeout,omitempty"`
	DisableRedirect bool              `json:"disableRedirect,omitempty"`
	UserAgent       string            `json:"userAgent,omitempty"`
	Fingerprint     map[string]string `json:"fingerprint,omitempty"`
	Browser         string            `json:"browser,omitempty"`
	Cookies         []interface{}     `json:"cookies,omitempty"`
}

// FastlsRPCClient RPC客户端
type FastlsRPCClient struct {
	RPCURL    string
	Client    *http.Client
	RequestID int
}

// NewFastlsRPCClient 创建新的RPC客户端
func NewFastlsRPCClient(rpcURL string) *FastlsRPCClient {
	return &FastlsRPCClient{
		RPCURL: rpcURL,
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
		RequestID: 0,
	}
}

// call 调用RPC方法
func (c *FastlsRPCClient) call(method string, params interface{}) (map[string]interface{}, error) {
	c.RequestID++

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      c.RequestID,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %v", err)
	}

	httpReq, err := http.NewRequest("POST", c.RPCURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %v", err)
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal response failed: %v", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error [%d]: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// Health 健康检查
func (c *FastlsRPCClient) Health() (map[string]interface{}, error) {
	return c.call("health", map[string]interface{}{})
}

// Fetch 发送HTTP请求
func (c *FastlsRPCClient) Fetch(params FetchParams) (map[string]interface{}, error) {
	return c.call("fetch", params)
}

func main() {
	// 创建客户端
	client := NewFastlsRPCClient("http://localhost:8801/rpc")

	// 1. 健康检查
	fmt.Println("1. 健康检查:")
	health, err := client.Health()
	if err != nil {
		fmt.Printf("   错误: %v\n\n", err)
	} else {
		fmt.Printf("   %v\n\n", health)
	}

	// 2. 简单GET请求
	fmt.Println("2. 简单GET请求:")
	result, err := client.Fetch(FetchParams{
		URL: "https://tls.peet.ws/api/all",
	})
	if err != nil {
		fmt.Printf("   错误: %v\n\n", err)
	} else {
		fmt.Printf("   Status: %.0f\n", result["status"])
		fmt.Printf("   OK: %v\n", result["ok"])
		if body, ok := result["body"].(string); ok {
			fmt.Printf("   Body length: %d bytes\n\n", len(body))
		}
	}

	// 3. 使用浏览器指纹
	fmt.Println("3. 使用Chrome142指纹:")
	result, err = client.Fetch(FetchParams{
		URL:     "https://tls.peet.ws/api/all",
		Browser: "chrome142",
	})
	if err != nil {
		fmt.Printf("   错误: %v\n\n", err)
	} else {
		fmt.Printf("   Status: %.0f\n", result["status"])
		fmt.Printf("   OK: %v\n\n", result["ok"])
	}

	// 4. 使用自定义JA3指纹
	fmt.Println("4. 使用自定义JA3指纹:")
	result, err = client.Fetch(FetchParams{
		URL: "https://tls.peet.ws/api/all",
		Fingerprint: map[string]string{
			"type":  "ja3",
			"value": "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-27-17513,29-23-24,0",
		},
	})
	if err != nil {
		fmt.Printf("   错误: %v\n\n", err)
	} else {
		fmt.Printf("   Status: %.0f\n", result["status"])
		fmt.Printf("   OK: %v\n\n", result["ok"])
	}

	// 5. POST请求
	fmt.Println("5. POST请求:")
	result, err = client.Fetch(FetchParams{
		URL:    "https://httpbin.org/post",
		Method: "POST",
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: `{"key": "value"}`,
	})
	if err != nil {
		fmt.Printf("   错误: %v\n\n", err)
	} else {
		fmt.Printf("   Status: %.0f\n", result["status"])
		fmt.Printf("   OK: %v\n\n", result["ok"])
	}

	// 6. 使用代理（示例）
	fmt.Println("6. 使用代理（示例）:")
	fmt.Println("   (需要配置代理服务器)\n")
}
