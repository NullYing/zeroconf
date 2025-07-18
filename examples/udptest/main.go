package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"sync"
	"syscall"

	"github.com/miekg/dns"
)

// 平台特定的SO_REUSEPORT常量定义
var soReusePort int

func init() {
	switch runtime.GOOS {
	case "linux":
		soReusePort = 15 // Linux上的SO_REUSEPORT值
	case "darwin", "freebsd", "netbsd", "openbsd":
		soReusePort = syscall.SO_REUSEPORT // Unix系统使用syscall包中的常量
	default:
		soReusePort = -1 // 不支持的平台
	}
}

// setReusePort 跨平台设置端口复用选项
func setReusePort(network, address string, c syscall.RawConn) error {
	var opErr error
	err := c.Control(func(fd uintptr) {
		socketFd := int(fd)

		// 设置 SO_REUSEADDR 选项（所有平台都支持）
		opErr = syscall.SetsockoptInt(socketFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		if opErr != nil {
			logger.Printf("设置SO_REUSEADDR失败: %v", opErr)
			return
		}

		// 设置 SO_REUSEPORT 选项（仅在支持的平台上）
		if soReusePort != -1 {
			opErr = syscall.SetsockoptInt(socketFd, syscall.SOL_SOCKET, soReusePort, 1)
			if opErr != nil {
				logger.Printf("设置SO_REUSEPORT失败: %v (这在某些系统上是正常的)", opErr)
				// 如果SO_REUSEPORT失败，不返回错误，只有SO_REUSEADDR是必需的
				opErr = nil
			}
		} else {
			logger.Printf("当前平台 (%s) 不支持SO_REUSEPORT", runtime.GOOS)
		}
	})
	if err != nil {
		return err
	}
	return opErr
}

// getAllNetworkInterfaces 获取所有可用的网络接口和IP地址
func getAllNetworkInterfaces() ([]net.IP, error) {
	var ips []net.IP

	// 获取所有网络接口
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("获取网络接口失败: %v", err)
	}

	for _, iface := range interfaces {
		// 跳过无效和loopback接口
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		// 获取接口的地址
		addrs, err := iface.Addrs()
		if err != nil {
			logger.Printf("获取接口 %s 的地址失败: %v", iface.Name, err)
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// 只使用IPv4地址，跳过loopback地址
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}

			ips = append(ips, ip)
			fmt.Printf("发现网卡 %s 的IPv4地址: %s\n", iface.Name, ip.String())
		}
	}

	return ips, nil
}

// listenOnInterface 在指定的IP地址上监听UDP
func listenOnInterface(ip net.IP, port int, wg *sync.WaitGroup) {
	defer wg.Done()

	unicast := net.UDPAddr{
		IP:   ip,
		Port: port,
	}

	// 使用ListenConfig来创建连接，以便设置端口复用选项
	lc := net.ListenConfig{
		Control: setReusePort,
	}

	fmt.Printf("正在尝试监听地址: %s\n", unicast.String())
	uConn, err := lc.ListenPacket(context.Background(), "udp4", unicast.String())
	if err != nil {
		logger.Printf("❌ 在 %s 上监听失败: %v", unicast.String(), err)
		return
	}

	defer uConn.Close()

	// 转换为UDPConn类型以使用UDP特定的方法
	udpConn := uConn.(*net.UDPConn)

	// 获取实际监听的地址
	localAddr := udpConn.LocalAddr()
	fmt.Printf("✅ 成功监听IPv4单播UDP，实际地址: %s\n", localAddr.String())

	// 创建缓冲区接收数据
	buffer := make([]byte, 4096)

	for {
		// 接收UDP数据
		n, clientAddr, err := udpConn.ReadFromUDP(buffer)
		if err != nil {
			logger.Printf("❌ 在 %s 上读取UDP数据失败: %v", localAddr.String(), err)
			continue
		}

		fmt.Printf("\n📦 [%s] 收到来自 %s 的数据，长度: %d 字节\n", localAddr.String(), clientAddr, n)

		// 尝试解析为DNS消息
		msg := new(dns.Msg)
		err = msg.Unpack(buffer[:n])
		if err != nil {
			fmt.Printf("⚠️  [%s] 不是有效的DNS消息: %v\n", localAddr.String(), err)
			fmt.Printf("[%s] 这可能是一个普通的UDP数据包\n", localAddr.String())
			fmt.Printf("[%s] 原始数据 (前100字节): %q\n", localAddr.String(), string(buffer[:min(n, 100)]))
		} else {
			fmt.Printf("✅ [%s] 成功解析为DNS消息:\n", localAddr.String())
			fmt.Println(msg)
		}

		fmt.Println("----------------------------------------")
	}
}

func main() {
	port := 5353

	fmt.Println("🔍 正在扫描所有网络接口...")

	// 获取所有可用的IP地址
	ips, err := getAllNetworkInterfaces()
	if err != nil {
		fmt.Printf("❌ 获取网络接口失败: %v\n", err)
		os.Exit(1)
	}

	if len(ips) == 0 {
		fmt.Println("❌ 没有找到可用的IPv4网络接口")
		os.Exit(1)
	}

	fmt.Printf("\n🌐 找到 %d 个可用的IPv4接口\n", len(ips))
	fmt.Printf("📡 将在端口 %d 上监听所有接口\n\n", port)

	// 使用WaitGroup来等待所有goroutine
	var wg sync.WaitGroup

	// 在每个IP地址上启动监听
	for _, ip := range ips {
		wg.Add(1)
		go listenOnInterface(ip, port, &wg)
	}

	fmt.Println("📡 所有监听器已启动")
	fmt.Println("等待接收数据包...")
	fmt.Println("提示：可以使用以下命令测试发送数据包:")
	for _, ip := range ips {
		fmt.Printf("echo 'test' | nc -u %s %d\n", ip.String(), port)
	}
	fmt.Println("\n=== 开始监听所有网卡 ===")

	// 等待所有goroutine完成（实际上会一直运行）
	wg.Wait()
}
