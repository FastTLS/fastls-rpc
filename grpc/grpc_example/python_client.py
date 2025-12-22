#!/usr/bin/env python3
"""
Fastls gRPC Python客户端示例
"""

import grpc
from fastls_pb2 import HealthRequest, FetchRequest, Fingerprint
from fastls_pb2_grpc import FastlsServiceStub


def main():
    # 连接到gRPC服务器
    channel = grpc.insecure_channel('localhost:8802')
    stub = FastlsServiceStub(channel)

    # 1. 健康检查
    print("1. 健康检查:")
    try:
        health_resp = stub.Health(HealthRequest())
        print(f"   Status: {health_resp.status}\n")
    except Exception as e:
        print(f"   错误: {e}\n")

    # 2. 简单GET请求
    print("2. 简单GET请求:")
    try:
        fetch_resp = stub.Fetch(FetchRequest(
            url="https://tls.peet.ws/api/all"
        ))
        print(f"   Status: {fetch_resp.status}")
        print(f"   OK: {fetch_resp.ok}")
        print(f"   Body length: {len(fetch_resp.body)} bytes\n")
    except Exception as e:
        print(f"   错误: {e}\n")

    # 3. 使用浏览器指纹
    print("3. 使用Chrome142指纹:")
    try:
        fetch_resp = stub.Fetch(FetchRequest(
            url="https://tls.peet.ws/api/all",
            browser="chrome142"
        ))
        print(f"   Status: {fetch_resp.status}")
        print(f"   OK: {fetch_resp.ok}\n")
    except Exception as e:
        print(f"   错误: {e}\n")

    # 4. 使用自定义JA3指纹
    print("4. 使用自定义JA3指纹:")
    try:
        fetch_resp = stub.Fetch(FetchRequest(
            url="https://tls.peet.ws/api/all",
            fingerprint=Fingerprint(
                type="ja3",
                value="771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-27-17513,29-23-24,0"
            )
        ))
        print(f"   Status: {fetch_resp.status}")
        print(f"   OK: {fetch_resp.ok}\n")
    except Exception as e:
        print(f"   错误: {e}\n")

    # 5. POST请求
    print("5. POST请求:")
    try:
        fetch_resp = stub.Fetch(FetchRequest(
            url="https://httpbin.org/post",
            method="POST",
            headers={"Content-Type": "application/json"},
            body='{"key": "value"}'
        ))
        print(f"   Status: {fetch_resp.status}")
        print(f"   OK: {fetch_resp.ok}\n")
    except Exception as e:
        print(f"   错误: {e}\n")

    channel.close()


if __name__ == "__main__":
    main()

