package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"syscall"
	"time"

	"github.com/NullYing/zeroconf"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var (
	service  = flag.String("service", "_ipp._tcp", "Set the service category to look for devices.")
	domain   = flag.String("domain", "local", "Set the search domain. For local networks, default is fine.")
	waitTime = flag.Int("wait", 20, "Duration in [s] to run discovery.")
)

// reusePortControl sets socket port reuse option for cross-platform compatibility
func reusePortControl(network, address string, c syscall.RawConn) error {
	var opErr error
	err := c.Control(func(fd uintptr) {
		socketFd := int(fd)
		// Set SO_REUSEADDR option (supported on all platforms)
		opErr = syscall.SetsockoptInt(socketFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		if opErr != nil {
			return
		}

		// Set SO_REUSEPORT option (Unix-like systems only)
		// On Windows, this will fail silently, which is acceptable
		_ = syscall.SetsockoptInt(socketFd, syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
	})
	if err != nil {
		return err
	}
	return opErr
}

// createCustomIPv4Conn creates a custom IPv4 multicast connection
func createCustomIPv4Conn(ifaces []net.Interface) (*ipv4.PacketConn, error) {
	mdnsWildcardAddrIPv4 := &net.UDPAddr{
		IP:   net.ParseIP("224.0.0.0"),
		Port: 5353,
	}

	lc := &net.ListenConfig{
		Control: reusePortControl,
	}

	conn, err := lc.ListenPacket(context.Background(), "udp4", mdnsWildcardAddrIPv4.String())
	if err != nil {
		return nil, fmt.Errorf("failed to listen on IPv4: %v", err)
	}

	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("expected *net.UDPConn, got %T", conn)
	}

	// Set read buffer size
	if err := udpConn.SetReadBuffer(1024 * 1024); err != nil {
		log.Printf("[WARN] Failed to set read buffer: %v", err)
	}

	// Create IPv4 PacketConn
	pkConn := ipv4.NewPacketConn(udpConn)
	pkConn.SetControlMessage(ipv4.FlagInterface, true)
	pkConn.SetControlMessage(ipv4.FlagDst, true)
	_ = pkConn.SetMulticastTTL(255)

	// Join multicast groups only on interfaces that support IPv4
	mdnsGroupIPv4 := net.IPv4(224, 0, 0, 251)
	var failedJoins int
	var attemptedJoins int
	for _, iface := range ifaces {
		// Skip interfaces that don't support IPv4
		if !interfaceSupportsIPv4(&iface) {
			continue
		}
		attemptedJoins++
		if err := pkConn.JoinGroup(&iface, &net.UDPAddr{IP: mdnsGroupIPv4}); err != nil {
			log.Printf("[WARN] Failed to join IPv4 multicast group on interface %s: %v", iface.Name, err)
			failedJoins++
		}
	}

	if attemptedJoins == 0 {
		pkConn.Close()
		return nil, fmt.Errorf("no IPv4-capable interfaces found")
	}
	if failedJoins == attemptedJoins {
		pkConn.Close()
		return nil, fmt.Errorf("failed to join any IPv4 multicast groups")
	}

	return pkConn, nil
}

// createCustomIPv6Conn creates a custom IPv6 multicast connection
func createCustomIPv6Conn(ifaces []net.Interface) (*ipv6.PacketConn, error) {
	mdnsWildcardAddrIPv6 := &net.UDPAddr{
		IP:   net.IPv6zero,
		Port: 5353,
	}

	lc := &net.ListenConfig{
		Control: reusePortControl,
	}

	conn, err := lc.ListenPacket(context.Background(), "udp6", mdnsWildcardAddrIPv6.String())
	if err != nil {
		return nil, fmt.Errorf("failed to listen on IPv6: %v", err)
	}

	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("expected *net.UDPConn, got %T", conn)
	}

	// Set read buffer size
	if err := udpConn.SetReadBuffer(1024 * 1024); err != nil {
		log.Printf("[WARN] Failed to set read buffer: %v", err)
	}

	// Create IPv6 PacketConn
	pkConn := ipv6.NewPacketConn(udpConn)
	pkConn.SetControlMessage(ipv6.FlagInterface, true)
	pkConn.SetControlMessage(ipv6.FlagDst, true)
	_ = pkConn.SetMulticastHopLimit(255)

	// Join multicast groups only on interfaces that support IPv6
	mdnsGroupIPv6 := net.ParseIP("ff02::fb")
	var failedJoins int
	var attemptedJoins int
	for _, iface := range ifaces {
		// Skip interfaces that don't support IPv6
		if !interfaceSupportsIPv6(&iface) {
			continue
		}
		attemptedJoins++
		if err := pkConn.JoinGroup(&iface, &net.UDPAddr{IP: mdnsGroupIPv6}); err != nil {
			log.Printf("[WARN] Failed to join IPv6 multicast group on interface %s: %v", iface.Name, err)
			failedJoins++
		}
	}

	if attemptedJoins == 0 {
		pkConn.Close()
		return nil, fmt.Errorf("no IPv6-capable interfaces found")
	}
	if failedJoins == attemptedJoins {
		pkConn.Close()
		return nil, fmt.Errorf("failed to join any IPv6 multicast groups")
	}

	return pkConn, nil
}

// interfaceSupportsIPv4 checks if an interface supports IPv4
func interfaceSupportsIPv4(iface *net.Interface) bool {
	addrs, err := iface.Addrs()
	if err != nil {
		return false
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
		if ip.To4() != nil {
			return true
		}
	}
	return false
}

// interfaceSupportsIPv6 checks if an interface supports IPv6
func interfaceSupportsIPv6(iface *net.Interface) bool {
	addrs, err := iface.Addrs()
	if err != nil {
		return false
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
		if ip.To4() == nil && ip.To16() != nil {
			return true
		}
	}
	return false
}

// listMulticastInterfaces returns a list of multicast-capable interfaces
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

func main() {
	flag.Parse()

	log.Println("=== Custom Connection Example ===")
	log.Println("This example demonstrates how to use third-party managed connections with zeroconf resolver.")
	log.Println()

	// Get multicast interfaces
	ifaces := listMulticastInterfaces()
	if len(ifaces) == 0 {
		log.Fatalln("No multicast interfaces found")
	}

	log.Printf("Found %d multicast interface(s)\n", len(ifaces))

	// Create custom IPv4 connection
	log.Println("Creating custom IPv4 connection...")
	ipv4Conn, err := createCustomIPv4Conn(ifaces)
	if err != nil {
		log.Printf("[WARN] Failed to create IPv4 connection: %v", err)
		log.Println("Continuing with IPv6 only...")
		ipv4Conn = nil
	} else {
		log.Println("✓ IPv4 connection created successfully")
	}

	// Create custom IPv6 connection
	log.Println("Creating custom IPv6 connection...")
	ipv6Conn, err := createCustomIPv6Conn(ifaces)
	if err != nil {
		log.Printf("[WARN] Failed to create IPv6 connection: %v", err)
		log.Println("Continuing with IPv4 only...")
		ipv6Conn = nil
	} else {
		log.Println("✓ IPv6 connection created successfully")
	}

	if ipv4Conn == nil && ipv6Conn == nil {
		log.Fatalln("Failed to create any connections")
	}

	// Ensure connections are closed when we're done
	// Note: These connections are managed by us, not by the resolver
	defer func() {
		log.Println("\nClosing custom connections...")
		if ipv4Conn != nil {
			if err := ipv4Conn.Close(); err != nil {
				log.Printf("[WARN] Error closing IPv4 connection: %v", err)
			} else {
				log.Println("✓ IPv4 connection closed")
			}
		}
		if ipv6Conn != nil {
			if err := ipv6Conn.Close(); err != nil {
				log.Printf("[WARN] Error closing IPv6 connection: %v", err)
			} else {
				log.Println("✓ IPv6 connection closed")
			}
		}
	}()

	// Create resolver with custom connections
	// The resolver will use our connections but won't close them
	log.Println("\nCreating resolver with custom connections...")
	resolver, err := zeroconf.NewResolver(
		zeroconf.WithCustomConn(ipv4Conn, ipv6Conn, nil, nil),
	)
	if err != nil {
		log.Fatalln("Failed to create resolver:", err)
	}
	log.Println("✓ Resolver created successfully")

	// Create channel for service entries
	entries := make(chan *zeroconf.ServiceEntry)
	go func(results <-chan *zeroconf.ServiceEntry) {
		log.Println("\n=== Service Discovery Results ===")
		count := 0
		for entry := range results {
			count++
			log.Printf("\n[%d] Service Found:", count)
			log.Printf("  Instance: %s", entry.Instance)
			log.Printf("  Service: %s", entry.Service)
			log.Printf("  Domain: %s", entry.Domain)
			log.Printf("  Host: %s", entry.HostName)
			log.Printf("  Port: %d", entry.Port)
			if len(entry.AddrIPv4) > 0 {
				log.Printf("  IPv4: %v", entry.AddrIPv4)
			}
			if len(entry.AddrIPv6) > 0 {
				log.Printf("  IPv6: %v", entry.AddrIPv6)
			}
			if len(entry.Text) > 0 {
				log.Printf("  Text: %v", entry.Text)
			}
		}
		log.Printf("\n=== Discovery Complete (Total: %d services) ===\n", count)
	}(entries)

	// Start browsing
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(*waitTime))
	defer cancel()

	log.Printf("\nStarting service discovery for '%s' in domain '%s' (timeout: %ds)...\n", *service, *domain, *waitTime)
	err = resolver.Browse(ctx, *service, *domain, []string{}, entries)
	if err != nil {
		log.Fatalln("Failed to browse:", err)
	}

	// Wait for context to expire
	<-ctx.Done()
	log.Println("\nDiscovery timeout reached, waiting for cleanup...")

	// Wait a bit for cleanup
	time.Sleep(1 * time.Second)

	log.Println("\n=== Example Complete ===")
	log.Println("Note: The custom connections were closed by us, not by the resolver.")
}
