#!/usr/bin/env python3
"""
Fastls RPC Python客户端示例
"""

import json
import requests
from typing import Optional, Dict, Any


class FastlsRPCClient:
    """Fastls RPC客户端"""
    
    def __init__(self, rpc_url: str = "http://localhost:8801/rpc"):
        """
        初始化RPC客户端
        
        Args:
            rpc_url: RPC服务器地址
        """
        self.rpc_url = rpc_url
        self.request_id = 0
    
    def _next_id(self) -> int:
        """生成下一个请求ID"""
        self.request_id += 1
        return self.request_id
    
    def _call(self, method: str, params: Dict[str, Any]) -> Dict[str, Any]:
        """
        调用RPC方法
        
        Args:
            method: 方法名
            params: 方法参数
            
        Returns:
            RPC响应结果
        """
        request = {
            "jsonrpc": "2.0",
            "method": method,
            "params": params,
            "id": self._next_id()
        }
        
        response = requests.post(
            self.rpc_url,
            json=request,
            headers={"Content-Type": "application/json"}
        )
        response.raise_for_status()
        
        result = response.json()
        
        if "error" in result:
            raise Exception(f"RPC Error: {result['error']}")
        
        return result.get("result", {})
    
    def health(self) -> Dict[str, str]:
        """健康检查"""
        return self._call("health", {})
    
    def fetch(
        self,
        url: str,
        method: str = "GET",
        headers: Optional[Dict[str, str]] = None,
        body: str = "",
        proxy: Optional[str] = None,
        timeout: int = 30,
        disable_redirect: bool = False,
        user_agent: Optional[str] = None,
        fingerprint: Optional[Dict[str, str]] = None,
        browser: Optional[str] = None,
        cookies: Optional[list] = None
    ) -> Dict[str, Any]:
        """
        发送HTTP请求
        
        Args:
            url: 请求URL
            method: HTTP方法
            headers: 请求头
            body: 请求体
            proxy: 代理地址
            timeout: 超时时间（秒）
            disable_redirect: 是否禁用重定向
            user_agent: User-Agent
            fingerprint: 指纹配置 {"type": "ja3", "value": "..."} 或 {"type": "ja4r", "value": "..."}
            browser: 浏览器类型
            cookies: Cookie列表
            
        Returns:
            响应结果
        """
        params = {
            "url": url,
            "method": method,
        }
        
        if headers:
            params["headers"] = headers
        if body:
            params["body"] = body
        if proxy:
            params["proxy"] = proxy
        if timeout:
            params["timeout"] = timeout
        if disable_redirect:
            params["disableRedirect"] = disable_redirect
        if user_agent:
            params["userAgent"] = user_agent
        if fingerprint:
            params["fingerprint"] = fingerprint
        if browser:
            params["browser"] = browser
        if cookies:
            params["cookies"] = cookies
        
        return self._call("fetch", params)


def main():
    """示例用法"""
    # 创建客户端
    client = FastlsRPCClient("http://localhost:8801/rpc")
    
    # 1. 健康检查
    print("1. 健康检查:")
    health = client.health()
    print(f"   {health}\n")
    
    # 2. 简单GET请求
    print("2. 简单GET请求:")
    result = client.fetch("https://tls.peet.ws/api/all")
    print(f"   Status: {result['status']}")
    print(f"   OK: {result['ok']}")
    print(f"   Body length: {len(result['body'])} bytes\n")
    
    # 3. 使用浏览器指纹
    print("3. 使用Chrome142指纹:")
    result = client.fetch(
        "https://tls.peet.ws/api/all",
        browser="chrome142"
    )
    print(f"   Status: {result['status']}")
    print(f"   OK: {result['ok']}\n")
    
    # 4. 使用自定义JA3指纹
    print("4. 使用自定义JA3指纹:")
    result = client.fetch(
        "https://tls.peet.ws/api/all",
        fingerprint={
            "type": "ja3",
            "value": "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-27-17513,29-23-24,0"
        }
    )
    print(f"   Status: {result['status']}")
    print(f"   OK: {result['ok']}\n")
    
    # 5. POST请求
    print("5. POST请求:")
    result = client.fetch(
        "https://httpbin.org/post",
        method="POST",
        headers={"Content-Type": "application/json"},
        body='{"key": "value"}'
    )
    print(f"   Status: {result['status']}")
    print(f"   OK: {result['ok']}\n")
    
    # 6. 使用代理
    print("6. 使用代理（示例）:")
    # result = client.fetch(
    #     "https://httpbin.org/ip",
    #     proxy="http://127.0.0.1:1080"
    # )
    # print(f"   Status: {result['status']}")
    # print(f"   OK: {result['ok']}\n")
    print("   (需要配置代理服务器)\n")


if __name__ == "__main__":
    main()

