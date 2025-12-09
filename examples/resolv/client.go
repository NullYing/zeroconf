package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/NullYing/zeroconf"
)

var (
	service  = flag.String("service", "_workstation._tcp", "Set the service category to look for devices.")
	domain   = flag.String("domain", "local", "Set the search domain. For local networks, default is fine.")
	waitTime = flag.Int("wait", 20, "Duration in [s] to run discovery.")
)

func main() {
	flag.Parse()
	// Discover all services on the network (e.g. _workstation._tcp)

	// Example 1: Use default connections (original behavior)
	// The resolver will create and manage its own connections
	resolver, err := zeroconf.NewResolver(zeroconf.SelectIPTraffic(zeroconf.IPv4AndIPv6), zeroconf.EnableUnicast(true))

	// Example 2: Use custom connections (third-party managed)
	// If you want to use your own connections, you can do:
	//  1. Create your own IPv4/IPv6 PacketConn connections
	//  2. Pass them via WithCustomConn option
	//  3. Manage their lifecycle yourself (they won't be closed by resolver)
	//
	// Example code (requires additional imports: "net", "golang.org/x/net/ipv4", "golang.org/x/net/ipv6"):
	//   lc := &net.ListenConfig{}
	//   conn4, _ := lc.ListenPacket(context.Background(), "udp4", "224.0.0.0:5353")
	//   udpConn4, _ := conn4.(*net.UDPConn)
	//   ipv4Conn := ipv4.NewPacketConn(udpConn4)
	//   conn6, _ := lc.ListenPacket(context.Background(), "udp6", "[::]:5353")
	//   udpConn6, _ := conn6.(*net.UDPConn)
	//   ipv6Conn := ipv6.NewPacketConn(udpConn6)
	//   resolver, err := zeroconf.NewResolver(zeroconf.WithCustomConn(ipv4Conn, ipv6Conn, nil, nil))
	//   defer ipv4Conn.Close()
	//   defer ipv6Conn.Close()

	if err != nil {
		log.Fatalln("Failed to initialize resolver:", err.Error())
	}

	entries := make(chan *zeroconf.ServiceEntry)
	go func(results <-chan *zeroconf.ServiceEntry) {
		for entry := range results {
			log.Println(entry)
		}
		log.Println("No more entries.")
	}(entries)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(*waitTime))
	defer cancel()
	//err = resolver.Browse(ctx, *service, *domain, []string{"_universal._sub._ipps._tcp.local"}, entries)
	err = resolver.Browse(ctx, *service, *domain, []string{}, entries)
	if err != nil {
		log.Fatalln("Failed to browse:", err.Error())
	}

	<-ctx.Done()
	// Wait some additional time to see debug messages on go routine shutdown.
	time.Sleep(1 * time.Second)
}
