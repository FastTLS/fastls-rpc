package main

import (
	"context"
	"fmt"
	"log"
	"time"

	pb "github.com/FastTLS/fastls-rpc/grpc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// 连接到gRPC服务器
	conn, err := grpc.Dial("localhost:8802", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()

	// 创建客户端
	client := pb.NewFastlsServiceClient(conn)

	// 创建上下文
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. 健康检查
	fmt.Println("1. 健康检查:")
	healthResp, err := client.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		fmt.Printf("   错误: %v\n\n", err)
	} else {
		fmt.Printf("   Status: %s\n\n", healthResp.Status)
	}

	// 2. 简单GET请求
	fmt.Println("2. 简单GET请求:")
	fetchResp, err := client.Fetch(ctx, &pb.FetchRequest{
		Url: "https://tls.peet.ws/api/all",
	})
	if err != nil {
		fmt.Printf("   错误: %v\n\n", err)
	} else {
		fmt.Printf("   Status: %d\n", fetchResp.Status)
		fmt.Printf("   OK: %v\n", fetchResp.Ok)
		fmt.Printf("   Body length: %d bytes\n\n", len(fetchResp.Body))
	}

	// 3. 使用浏览器指纹
	fmt.Println("3. 使用Chrome142指纹:")
	fetchResp, err = client.Fetch(ctx, &pb.FetchRequest{
		Url:     "https://tls.peet.ws/api/all",
		Browser: "chrome142",
	})
	if err != nil {
		fmt.Printf("   错误: %v\n\n", err)
	} else {
		fmt.Printf("   Status: %d\n", fetchResp.Status)
		fmt.Printf("   OK: %v\n\n", fetchResp.Ok)
	}

	// 4. 使用自定义JA3指纹
	fmt.Println("4. 使用自定义JA3指纹:")
	fetchResp, err = client.Fetch(ctx, &pb.FetchRequest{
		Url: "https://tls.peet.ws/api/all",
		Fingerprint: &pb.Fingerprint{
			Type:  "ja3",
			Value: "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-27-17513,29-23-24,0",
		},
	})
	if err != nil {
		fmt.Printf("   错误: %v\n\n", err)
	} else {
		fmt.Printf("   Status: %d\n", fetchResp.Status)
		fmt.Printf("   OK: %v\n\n", fetchResp.Ok)
	}

	// 5. POST请求
	fmt.Println("5. POST请求:")
	fetchResp, err = client.Fetch(ctx, &pb.FetchRequest{
		Url:    "https://httpbin.org/post",
		Method: "POST",
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: `{"key": "value"}`,
	})
	if err != nil {
		fmt.Printf("   错误: %v\n\n", err)
	} else {
		fmt.Printf("   Status: %d\n", fetchResp.Status)
		fmt.Printf("   OK: %v\n\n", fetchResp.Ok)
	}
}
