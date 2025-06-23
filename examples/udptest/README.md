# UDP DNS 监听测试脚本

这个测试脚本用于监听IPv4的5353端口，接收单播UDP数据包并使用`github.com/miekg/dns`库解析DNS消息。

## 功能特点

- 只监听IPv4协议
- 监听5353端口（mDNS标准端口）
- 单播UDP通信（非多播）
- **启用端口复用** - 使用SO_REUSEADDR和SO_REUSEPORT选项
- 使用DNS库解析接收到的数据包
- 详细打印DNS消息的各个部分

## 端口复用功能

脚本使用了`setReusePort`函数来设置socket选项：

- **SO_REUSEADDR**: 允许重用处于TIME_WAIT状态的地址
- **SO_REUSEPORT**: 允许多个进程监听同一端口（在支持的系统上）

这使得多个程序实例可以同时监听5353端口，对于测试和调试非常有用。

## 运行方式

```bash
cd examples/udptest
go run main.go
```

## 输出说明

当接收到UDP数据包时，脚本会输出：

1. 数据包来源地址和大小
2. DNS消息的详细解析结果，包括：
   - 消息ID
   - 查询/响应类型
   - 操作码
   - 各种DNS标志位
   - 问题部分（查询内容）
   - 回答部分（响应记录）
   - 权威部分
   - 附加部分

如果数据包不是有效的DNS格式，会显示解析错误并打印原始十六进制数据。

## 测试用途

这个脚本主要用于：
- 调试mDNS通信
- 分析DNS数据包格式
- 测试单播DNS查询
- 网络故障排查

# 单播监听功能说明

本示例演示了如何使用zeroconf库的单播监听功能。

## 功能特性

- 支持在网卡IP地址上监听单播UDP流量
- 可通过配置选项开启/关闭单播监听
- 同时支持IPv4和IPv6单播监听
- 自动排除回环、多播和链路本地地址

## 使用方法

```go
// 启用单播监听
resolver, err := zeroconf.NewResolver(
    zeroconf.SelectIPTraffic(zeroconf.IPv4AndIPv6), 
    zeroconf.EnableUnicast(true),
)

// 禁用单播监听（默认）
resolver, err := zeroconf.NewResolver(
    zeroconf.SelectIPTraffic(zeroconf.IPv4AndIPv6), 
    zeroconf.EnableUnicast(false),
)
```

## 配置选项

- `EnableUnicast(true)`: 启用单播监听
- `EnableUnicast(false)`: 禁用单播监听（默认）
- `SelectIPTraffic()`: 选择监听的IP类型（IPv4、IPv6或两者）
- `SelectIfaces()`: 选择特定的网络接口

## 技术细节

- 单播监听器绑定到具体的网卡IP地址，而不是0.0.0.0
- 监听端口：5353（mDNS标准端口）
- 接收缓冲区大小：1MB
- 自动过滤无效的IP地址类型

## 示例代码

参见 `client.go` 文件中的完整示例。 