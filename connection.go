package zeroconf

import (
	"context"
	"fmt"
	"log"
	"net"
	"syscall"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var (
	// Multicast groups used by mDNS
	mdnsGroupIPv4 = net.IPv4(224, 0, 0, 251)
	mdnsGroupIPv6 = net.ParseIP("ff02::fb")

	// mDNS wildcard addresses
	mdnsWildcardAddrIPv4 = &net.UDPAddr{
		IP:   net.ParseIP("224.0.0.0"),
		Port: 5353,
	}
	mdnsWildcardAddrIPv6 = &net.UDPAddr{
		IP:   net.IPv6zero,
		Port: 5353,
	}

	// mDNS endpoint addresses
	ipv4Addr = &net.UDPAddr{
		IP:   mdnsGroupIPv4,
		Port: 5353,
	}
	ipv6Addr = &net.UDPAddr{
		IP:   mdnsGroupIPv6,
		Port: 5353,
	}
)

// reusePortControl 设置socket端口复用选项，兼容Windows系统
func reusePortControl(network, address string, c syscall.RawConn) error {
	return setReusePort(c)
}

func joinUdp6Multicast(interfaces []net.Interface) (*ipv6.PacketConn, error) {
	// 使用 ListenConfig 来支持端口复用
	lc := &net.ListenConfig{
		Control: reusePortControl,
	}

	conn, err := lc.ListenPacket(context.Background(), "udp6", mdnsWildcardAddrIPv6.String())
	if err != nil {
		return nil, err
	}

	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("expected *net.UDPConn, got %T", conn)
	}

	// 设置接收缓冲区大小
	if err := udpConn.SetReadBuffer(1024 * 1024); err != nil { // 1MB
		log.Printf("[WARN] Failed to set read buffer: %v", err)
	}

	// Join multicast groups to receive announcements
	pkConn := ipv6.NewPacketConn(udpConn)
	pkConn.SetControlMessage(ipv6.FlagInterface, true)
	pkConn.SetControlMessage(ipv6.FlagDst, true)

	_ = pkConn.SetMulticastHopLimit(255)

	if len(interfaces) == 0 {
		interfaces = listMulticastInterfaces()
	}
	// log.Println("Using multicast interfaces: ", interfaces)

	var failedJoins int
	for _, iface := range interfaces {
		if err := pkConn.JoinGroup(&iface, &net.UDPAddr{IP: mdnsGroupIPv6}); err != nil {
			// log.Println("Udp6 JoinGroup failed for iface ", iface)
			failedJoins++
		}
	}
	if failedJoins == len(interfaces) {
		pkConn.Close()
		return nil, fmt.Errorf("udp6: failed to join any of these interfaces: %v", interfaces)
	}

	return pkConn, nil
}

func joinUdp4Multicast(interfaces []net.Interface) (*ipv4.PacketConn, error) {
	// 使用 ListenConfig 来支持端口复用
	lc := &net.ListenConfig{
		Control: reusePortControl,
	}

	conn, err := lc.ListenPacket(context.Background(), "udp4", mdnsWildcardAddrIPv4.String())
	if err != nil {
		// log.Printf("[ERR] bonjour: Failed to bind to udp4 mutlicast: %v", err)
		return nil, err
	}

	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("expected *net.UDPConn, got %T", conn)
	}

	// 设置接收缓冲区大小以避免丢包
	if err := udpConn.SetReadBuffer(1024 * 1024); err != nil { // 1MB
		log.Printf("[WARN] Failed to set read buffer: %v", err)
	}

	// Join multicast groups to receive announcements
	pkConn := ipv4.NewPacketConn(udpConn)
	pkConn.SetControlMessage(ipv4.FlagInterface, true)
	pkConn.SetControlMessage(ipv4.FlagDst, true)
	_ = pkConn.SetMulticastTTL(255)

	if len(interfaces) == 0 {
		interfaces = listMulticastInterfaces()
	}
	// log.Println("Using multicast interfaces: ", interfaces)

	var failedJoins int
	for _, iface := range interfaces {
		if err := pkConn.JoinGroup(&iface, &net.UDPAddr{IP: mdnsGroupIPv4}); err != nil {
			// log.Println("Udp4 JoinGroup failed for iface ", iface)
			failedJoins++
		}
	}
	if failedJoins == len(interfaces) {
		pkConn.Close()
		return nil, fmt.Errorf("udp4: failed to join any of these interfaces: %v", interfaces)
	}

	return pkConn, nil
}

func listMulticastInterfaces() []net.Interface {
	var interfaces []net.Interface
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifaces {
		if (ifi.Flags & net.FlagUp) == 0 {
			continue
		}
		if (ifi.Flags & net.FlagMulticast) > 0 {
			interfaces = append(interfaces, ifi)
		}
	}

	return interfaces
}

// createUnicastListeners creates unicast UDP listeners on interface IPs
func createUnicastListeners(interfaces []net.Interface, listenIPv4, listenIPv6 bool) ([]*net.UDPConn, []*net.UDPConn, error) {
	var ipv4Listeners []*net.UDPConn
	var ipv6Listeners []*net.UDPConn

	if len(interfaces) == 0 {
		interfaces = listMulticastInterfaces()
	}

	// 使用 ListenConfig 来支持端口复用
	lc := &net.ListenConfig{
		Control: reusePortControl,
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			log.Printf("[WARN] Failed to get addresses for interface %s: %v", iface.Name, err)
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}

			// Skip loopback and non-unicast addresses
			if ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
				continue
			}

			if ip.To4() != nil && listenIPv4 {
				// IPv4 unicast listener with port reuse
				addr := &net.UDPAddr{IP: ip, Port: 5353}
				conn, err := lc.ListenPacket(context.Background(), "udp4", addr.String())
				if err != nil {
					log.Printf("[WARN] Failed to create IPv4 unicast listener on %s: %v", ip, err)
					continue
				}

				udpConn, ok := conn.(*net.UDPConn)
				if !ok {
					conn.Close()
					log.Printf("[WARN] Expected *net.UDPConn for IPv4 unicast listener on %s", ip)
					continue
				}

				// 设置接收缓冲区大小
				if err := udpConn.SetReadBuffer(1024 * 1024); err != nil { // 1MB
					log.Printf("[WARN] Failed to set read buffer for IPv4 unicast listener: %v", err)
				}

				ipv4Listeners = append(ipv4Listeners, udpConn)
				//log.Printf("[INFO] Created IPv4 unicast listener with port reuse on %s", ip)

			} else if ip.To4() == nil && listenIPv6 {
				// IPv6 unicast listener with port reuse
				addr := &net.UDPAddr{IP: ip, Port: 5353}
				conn, err := lc.ListenPacket(context.Background(), "udp6", addr.String())
				if err != nil {
					log.Printf("[WARN] Failed to create IPv6 unicast listener on %s: %v", ip, err)
					continue
				}

				udpConn, ok := conn.(*net.UDPConn)
				if !ok {
					conn.Close()
					log.Printf("[WARN] Expected *net.UDPConn for IPv6 unicast listener on %s", ip)
					continue
				}

				// 设置接收缓冲区大小
				if err := udpConn.SetReadBuffer(1024 * 1024); err != nil { // 1MB
					log.Printf("[WARN] Failed to set read buffer for IPv6 unicast listener: %v", err)
				}

				ipv6Listeners = append(ipv6Listeners, udpConn)
				log.Printf("[INFO] Created IPv6 unicast listener with port reuse on %s", ip)
			}
		}
	}

	return ipv4Listeners, ipv6Listeners, nil
}
