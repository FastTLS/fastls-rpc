# 开发分支响应契约与资源限制

本页描述P03-A开发源码，尚未进入正式Release。现有JSON字段和Protobuf字段号保留；新字段是增量扩展。

## 响应数据

- `headers`：保留旧的单值显示视图，不能用来还原重复字段。
- `header_values`：原始多值响应头，Set-Cookie等不合并成一个字符串。
- `body`：保留文本解码及原有媒体类型的显示行为；无效UTF-8使用替代字符。需要准确字节时不要使用该字段。
- `body_base64`：请求显式设置`include_body_base64=true`时，附带HTTP正文去除传输分块后、内容解压前的原始字节，使用标准Base64。它与原始响应头中的Content-Encoding对应。默认不附带该视图，避免扩大旧客户端的消息；空正文为该字段的空字符串（JSON可省略空字段）。gRPC客户端使用此选项时应按需提高最大接收消息字节数。
- `trailers`：读取原始正文到EOF后得到的Trailer；`protocol`与`final_url`报告实际最终协议和URL。
- `error_code`和`timeout_phase`：机器可读错误分类；原有`error`仍提供可读说明。

HTTP状态4xx/5xx仍可作为成功取得的HTTP响应返回，不等于传输失败。未知、截断或校验失败的内容编码返回`decode_error`，不会把损坏压缩内容伪装成正常文本。

## 服务启动选项

`-max-response-bytes N`对原始正文及每层解码后的正文分别限额，读取最多N+1字节即可确认超限；0表示不限制。最多接受4层内容编码。zstd在启用字节限额时另限制解码器内存，保留至少64MiB窗口预算；字节限额不等于整个进程的内存上限。Base64及JSON/Protobuf封装有额外开销。

`-max-inflight N`限制一个服务实例同时处理的fetch调用数，覆盖上游正文读取与解码；0表示不限制。满额立即返回`overloaded`，不会积累无限等待队列。取消、超时、解码失败或完成读取均释放名额；健康检查不占用fetch名额。JSON-RPC与gRPC若运行在不同进程，分别持有自己的限额。

以上两项默认都是0，参数只由运维在启动时设置，不从请求DTO读取。

P03-B4新增`-max-request-bytes N`：默认4194304（4MiB），0同样选择默认值，正值范围1..67108864（64MiB），不提供无限输入。非法配置在监听前拒绝。与地址策略独立，使用当前正式核心依赖也可生效。

Fetch/JSON-RPC按完整HTTP正文计费，包括JSON封装、转义、未知字段和尾部空白；chunked按去分块后的字节计费，不计HTTP头和分块元数据，不自动解压入站Content-Encoding。先有限缓存至EOF，再解析，避免解码器提前结束绕过限制；超限HTTP413（JSON-RPC为-32600，Fetch带request_too_large），不访问目标。Fetch在取得并发名额后读取正文；JSON-RPC的fetch名额仍只覆盖解析后的执行阶段，不是全局入站解析并发上限。

gRPC按完整protobuf消息计费（含字段封装），同时限制压缩消息及解压后消息；超限走ResourceExhausted协议错误，未进入Fetch执行。输入配额不改变响应/客户端接收上限，也不是进程总内存、连接数或速率限制。三入口字节口径不同，不应直接用同一个业务body长度替代消息大小。

## 错误分类

`request_error`表示未取得可用响应，`read_error`表示正文传输读取失败，`decode_error`表示内容编码无效，`response_too_large`表示正文超限，`overloaded`表示并发名额耗尽，`cancelled`表示取消，`timeout`同时附带`timeout_phase`（当前一次性服务请求主要为`total`）。可用的核心阶段错误也保留原阶段名。

Fetch在并发超限时使用HTTP503；JSON-RPC和gRPC沿用已有fetch结果封装，返回`ok=false`及相同`error_code`。输入校验、框架消息上限等协议层错误继续使用原协议错误通道。

## 当前边界

P03-B2后续开发源码已增加[目标地址策略](ADDRESS_POLICY.md)，仅在支持新能力的核心中可启用；拒绝新增`address_denied`分类。原P03-A响应扩展本身不提供访问控制，默认公开依赖仍不能启用策略。MITM开发策略模式已有请求/隧道并发和寿命限制，总流量配额和统一审计仍未完成。二进制请求输入、多值请求头等也不因响应字段扩展而自动成为各入口的共有能力。FFI已保留原生二进制正文和多值头；对照原始字节时须使用`decode_content=False`。
