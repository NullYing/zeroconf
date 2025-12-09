package zeroconf

import (
	"context"
	"fmt"
	"log"
	"net"
	"runtime"
	"strings"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// IPType specifies the IP traffic the client listens for.
// This does not guarantee that only mDNS entries of this sepcific
// type passes. E.g. typical mDNS packets distributed via IPv4, often contain
// both DNS A and AAAA entries.
type IPType uint8

// Options for IPType.
const (
	IPv4        = 0x01
	IPv6        = 0x02
	IPv4AndIPv6 = (IPv4 | IPv6) //< Default option.
)

type clientOpts struct {
	listenOn          IPType
	ifaces            []net.Interface
	enableUnicast     bool
	customIPv4Conn    *ipv4.PacketConn
	customIPv6Conn    *ipv6.PacketConn
	customIPv4Unicast []*net.UDPConn
	customIPv6Unicast []*net.UDPConn
}

// ClientOption fills the option struct to configure intefaces, etc.
type ClientOption func(*clientOpts)

// SelectIPTraffic selects the type of IP packets (IPv4, IPv6, or both) this
// instance listens for.
// This does not guarantee that only mDNS entries of this sepcific
// type passes. E.g. typical mDNS packets distributed via IPv4, may contain
// both DNS A and AAAA entries.
func SelectIPTraffic(t IPType) ClientOption {
	return func(o *clientOpts) {
		o.listenOn = t
	}
}

// SelectIfaces selects the interfaces to query for mDNS records
func SelectIfaces(ifaces []net.Interface) ClientOption {
	return func(o *clientOpts) {
		o.ifaces = ifaces
	}
}

// EnableUnicast enables unicast listening on network interface IPs
func EnableUnicast(enable bool) ClientOption {
	return func(o *clientOpts) {
		o.enableUnicast = enable
	}
}

// WithCustomConn allows providing custom network connections for mDNS operations.
// The provided connections will be used instead of creating new ones, and they
// will not be closed when the resolver shuts down, allowing external management
// of connection lifecycle.
// Parameters:
//   - ipv4Conn: Custom IPv4 multicast PacketConn (can be nil)
//   - ipv6Conn: Custom IPv6 multicast PacketConn (can be nil)
//   - ipv4Unicast: Custom IPv4 unicast UDP connections (can be nil)
//   - ipv6Unicast: Custom IPv6 unicast UDP connections (can be nil)
func WithCustomConn(ipv4Conn *ipv4.PacketConn, ipv6Conn *ipv6.PacketConn, ipv4Unicast []*net.UDPConn, ipv6Unicast []*net.UDPConn) ClientOption {
	return func(o *clientOpts) {
		o.customIPv4Conn = ipv4Conn
		o.customIPv6Conn = ipv6Conn
		o.customIPv4Unicast = ipv4Unicast
		o.customIPv6Unicast = ipv6Unicast
	}
}

// Resolver acts as entry point for service lookups and to browse the DNS-SD.
type Resolver struct {
	c *client
}

// NewResolver creates a new resolver and joins the UDP multicast groups to
// listen for mDNS messages.
func NewResolver(options ...ClientOption) (*Resolver, error) {
	// Apply default configuration and load supplied options.
	var conf = clientOpts{
		listenOn: IPv4AndIPv6,
	}
	for _, o := range options {
		if o != nil {
			o(&conf)
		}
	}

	c, err := newClient(conf)
	if err != nil {
		return nil, err
	}
	return &Resolver{
		c: c,
	}, nil
}

// Browse for all services of a given type in a given domain.
func (r *Resolver) Browse(ctx context.Context, service, domain string, subtypes []string, entries chan<- *ServiceEntry) error {
	params := defaultParams(service)
	if domain != "" {
		params.Domain = domain
	}
	params.Entries = entries
	params.Subtypes = subtypes
	params.isBrowsing = true
	ctx, cancel := context.WithCancel(ctx)
	go r.c.mainloop(ctx, params)

	err := r.c.query(params)
	if err != nil {
		cancel()
		return err
	}
	// If previous probe was ok, it should be fine now. In case of an error later on,
	// the entries' queue is closed.
	go func() {
		if err := r.c.periodicQuery(ctx, params); err != nil {
			cancel()
		}
	}()

	return nil
}

// Lookup a specific service by its name and type in a given domain.
func (r *Resolver) Lookup(ctx context.Context, instance, service, domain string, entries chan<- *ServiceEntry) error {
	params := defaultParams(service)
	params.Instance = instance
	if domain != "" {
		params.Domain = domain
	}
	params.Entries = entries
	ctx, cancel := context.WithCancel(ctx)
	go r.c.mainloop(ctx, params)
	err := r.c.query(params)
	if err != nil {
		// cancel mainloop
		cancel()
		return err
	}
	// If previous probe was ok, it should be fine now. In case of an error later on,
	// the entries' queue is closed.
	go func() {
		if err := r.c.periodicQuery(ctx, params); err != nil {
			cancel()
		}
	}()

	return nil
}

// defaultParams returns a default set of QueryParams.
func defaultParams(service string) *lookupParams {
	return newLookupParams("", service, "local", false, make(chan *ServiceEntry))
}

// Client structure encapsulates both IPv4/IPv6 UDP connections.
type client struct {
	ipv4conn        *ipv4.PacketConn
	ipv6conn        *ipv6.PacketConn
	ipv4unicastConn []*net.UDPConn
	ipv6unicastConn []*net.UDPConn
	ifaces          []net.Interface
	// Flags to indicate if connections are managed externally
	ipv4connManaged        bool
	ipv6connManaged        bool
	ipv4unicastConnManaged bool
	ipv6unicastConnManaged bool
}

// Client structure constructor
func newClient(opts clientOpts) (*client, error) {
	ifaces := opts.ifaces
	if len(ifaces) == 0 {
		ifaces = listMulticastInterfaces()
	}

	// Use custom connections if provided, otherwise create new ones
	var ipv4conn *ipv4.PacketConn
	var ipv4connManaged bool
	if opts.customIPv4Conn != nil {
		ipv4conn = opts.customIPv4Conn
		ipv4connManaged = true
	} else if (opts.listenOn & IPv4) > 0 {
		var err error
		ipv4conn, err = joinUdp4Multicast(ifaces)
		if err != nil {
			return nil, err
		}
		ipv4connManaged = false
	}

	var ipv6conn *ipv6.PacketConn
	var ipv6connManaged bool
	if opts.customIPv6Conn != nil {
		ipv6conn = opts.customIPv6Conn
		ipv6connManaged = true
	} else if (opts.listenOn & IPv6) > 0 {
		var err error
		ipv6conn, err = joinUdp6Multicast(ifaces)
		if err != nil {
			return nil, err
		}
		ipv6connManaged = false
	}

	// 创建单播监听连接或使用自定义连接
	var ipv4unicastConn []*net.UDPConn
	var ipv6unicastConn []*net.UDPConn
	var ipv4unicastConnManaged bool
	var ipv6unicastConnManaged bool
	if opts.customIPv4Unicast != nil || opts.customIPv6Unicast != nil {
		// Use custom unicast connections
		ipv4unicastConn = opts.customIPv4Unicast
		ipv6unicastConn = opts.customIPv6Unicast
		ipv4unicastConnManaged = true
		ipv6unicastConnManaged = true
	} else if opts.enableUnicast {
		listenIPv4 := (opts.listenOn & IPv4) > 0
		listenIPv6 := (opts.listenOn & IPv6) > 0
		var err error
		ipv4unicastConn, ipv6unicastConn, err = createUnicastListeners(ifaces, listenIPv4, listenIPv6)
		if err != nil {
			return nil, fmt.Errorf("failed to create unicast listeners: %v", err)
		}
		ipv4unicastConnManaged = false
		ipv6unicastConnManaged = false
	}

	return &client{
		ipv4conn:               ipv4conn,
		ipv6conn:               ipv6conn,
		ipv4unicastConn:        ipv4unicastConn,
		ipv6unicastConn:        ipv6unicastConn,
		ifaces:                 ifaces,
		ipv4connManaged:        ipv4connManaged,
		ipv6connManaged:        ipv6connManaged,
		ipv4unicastConnManaged: ipv4unicastConnManaged,
		ipv6unicastConnManaged: ipv6unicastConnManaged,
	}, nil
}

// Start listeners and waits for the shutdown signal from exit channel
func (c *client) mainloop(ctx context.Context, params *lookupParams) {
	// start listening for responses
	msgCh := make(chan *dnsMsg, 265)
	if c.ipv4conn != nil {
		go c.recv(ctx, c.ipv4conn, msgCh)
	}
	if c.ipv6conn != nil {
		go c.recv(ctx, c.ipv6conn, msgCh)
	}

	// 启动单播监听
	for _, conn := range c.ipv4unicastConn {
		go c.recvUnicast(ctx, conn, msgCh)
	}
	for _, conn := range c.ipv6unicastConn {
		go c.recvUnicast(ctx, conn, msgCh)
	}

	// Iterate through channels from listeners goroutines
	var entries, sentEntries map[string]*ServiceEntry
	sentEntries = make(map[string]*ServiceEntry)
	for {
		select {
		case <-ctx.Done():
			// Context expired. Notify subscriber that we are done here.
			params.done()
			c.shutdown()
			return
		case dnsMsgData := <-msgCh:
			msg := dnsMsgData.msg
			entries = make(map[string]*ServiceEntry)
			//fmt.Println("msg", msg)
			sections := append(msg.Answer, msg.Ns...)
			sections = append(sections, msg.Extra...)

			for _, answer := range sections {
				switch rr := answer.(type) {
				case *dns.PTR:
					if params.ServiceName() != rr.Hdr.Name {
						//fmt.Println("service name mismatch", rr.Hdr.Name)
						continue
					}
					if params.ServiceInstanceName() != "" && params.ServiceInstanceName() != rr.Ptr {
						//fmt.Println("service instance name mismatch", rr.Ptr)
						continue
					}
					if _, ok := entries[rr.Ptr]; !ok {
						entries[rr.Ptr] = NewServiceEntry(
							trimDot(strings.Replace(rr.Ptr, rr.Hdr.Name, "", -1)),
							params.Service,
							params.Domain)
					}
					entries[rr.Ptr].TTL = rr.Hdr.Ttl
				case *dns.SRV:
					if params.ServiceInstanceName() != "" && params.ServiceInstanceName() != rr.Hdr.Name {
						continue
					} else if !strings.HasSuffix(rr.Hdr.Name, params.ServiceName()) {
						continue
					}
					if _, ok := entries[rr.Hdr.Name]; !ok {
						entries[rr.Hdr.Name] = NewServiceEntry(
							trimDot(strings.Replace(rr.Hdr.Name, params.ServiceName(), "", 1)),
							params.Service,
							params.Domain)
					}
					if udpAddr, ok := dnsMsgData.src.(*net.UDPAddr); ok {
						entries[rr.Hdr.Name].SrcAddr = udpAddr.IP
					}
					entries[rr.Hdr.Name].HostName = rr.Target
					entries[rr.Hdr.Name].Port = int(rr.Port)
					entries[rr.Hdr.Name].TTL = rr.Hdr.Ttl
				case *dns.TXT:
					if params.ServiceInstanceName() != "" && params.ServiceInstanceName() != rr.Hdr.Name {
						continue
					} else if !strings.HasSuffix(rr.Hdr.Name, params.ServiceName()) {
						continue
					}
					if _, ok := entries[rr.Hdr.Name]; !ok {
						entries[rr.Hdr.Name] = NewServiceEntry(
							trimDot(strings.Replace(rr.Hdr.Name, params.ServiceName(), "", 1)),
							params.Service,
							params.Domain)
					}
					entries[rr.Hdr.Name].Text = rr.Txt
					entries[rr.Hdr.Name].TTL = rr.Hdr.Ttl
				}
			}
			// Associate IPs in a second round as other fields should be filled by now.
			for _, answer := range sections {
				switch rr := answer.(type) {
				case *dns.A:
					for k, e := range entries {
						if e.HostName == rr.Hdr.Name {
							entries[k].AddrIPv4 = append(entries[k].AddrIPv4, rr.A)
						}
					}
				case *dns.AAAA:
					for k, e := range entries {
						if e.HostName == rr.Hdr.Name {
							entries[k].AddrIPv6 = append(entries[k].AddrIPv6, rr.AAAA)
						}
					}
				}
			}
		}

		if len(entries) > 0 {
			for k, e := range entries {
				if e.TTL == 0 {
					delete(entries, k)
					delete(sentEntries, k)
					continue
				}
				if _, ok := sentEntries[k]; ok {
					continue
				}

				// If this is an DNS-SD query do not throw PTR away.
				// It is expected to have only PTR for enumeration
				if params.ServiceRecord.ServiceTypeName() != params.ServiceRecord.ServiceName() {
					// Require at least one resolved IP address for ServiceEntry
					// TODO: wait some more time as chances are high both will arrive.
					if len(e.AddrIPv4) == 0 && len(e.AddrIPv6) == 0 {
						if len(e.SrcAddr) == 0 {
							continue
						}
						// 如果没有ip地址，认为来源的ip就是地址
						e.AddrIPv4 = append(e.AddrIPv4, e.SrcAddr)
					}
				}
				// Submit entry to subscriber and cache it.
				// This is also a point to possibly stop probing actively for a
				// service entry.
				params.Entries <- e
				sentEntries[k] = e
				if !params.isBrowsing {
					params.disableProbing()
				}
			}
		}
	}
}

// Shutdown client will close currently open connections and channel implicitly.
// Connections managed externally (via WithCustomConn) will not be closed.
func (c *client) shutdown() {
	if c.ipv4conn != nil && !c.ipv4connManaged {
		c.ipv4conn.Close()
	}
	if c.ipv6conn != nil && !c.ipv6connManaged {
		c.ipv6conn.Close()
	}

	// 关闭单播连接（仅关闭内部管理的连接）
	if !c.ipv4unicastConnManaged {
		for _, conn := range c.ipv4unicastConn {
			if conn != nil {
				conn.Close()
			}
		}
	}
	if !c.ipv6unicastConnManaged {
		for _, conn := range c.ipv6unicastConn {
			if conn != nil {
				conn.Close()
			}
		}
	}
}

type dnsMsg struct {
	msg *dns.Msg
	src net.Addr
}

// Data receiving routine reads from connection, unpacks packets into dns.Msg
// structures and sends them to a given msgCh channel
func (c *client) recv(ctx context.Context, l interface{}, msgCh chan *dnsMsg) {
	var readFrom func([]byte) (n int, src net.Addr, err error)

	switch pConn := l.(type) {
	case *ipv6.PacketConn:
		readFrom = func(b []byte) (n int, src net.Addr, err error) {
			n, _, src, err = pConn.ReadFrom(b)
			return
		}
	case *ipv4.PacketConn:
		readFrom = func(b []byte) (n int, src net.Addr, err error) {
			n, _, src, err = pConn.ReadFrom(b)
			return
		}

	default:
		return
	}

	buf := make([]byte, 65536)
	var fatalErr error
	for {
		// Handles the following cases:
		// - ReadFrom aborts with error due to closed UDP connection -> causes ctx cancel
		// - ReadFrom aborts otherwise.
		// TODO: the context check can be removed. Verify!
		if ctx.Err() != nil || fatalErr != nil {
			return
		}

		n, src, err := readFrom(buf)
		if err != nil {
			fatalErr = err
			continue
		}
		msg := new(dns.Msg)
		if err := msg.Unpack(buf[:n]); err != nil {
			log.Printf("[WARN] mdns: [%s] Failed to unpack packet: %v", src, err)
			continue
		}
		dMsg := &dnsMsg{msg: msg, src: src}
		select {
		case msgCh <- dMsg:
			//fmt.Println(src, msg)

			// Submit decoded DNS message and continue.
		case <-ctx.Done():
			// Abort.
			return
		}
	}
}

// recvUnicast receives data from unicast UDP connections
func (c *client) recvUnicast(ctx context.Context, conn *net.UDPConn, msgCh chan *dnsMsg) {
	buf := make([]byte, 65536)
	var fatalErr error
	for {
		// Handles the following cases:
		// - ReadFromUDP aborts with error due to closed UDP connection -> causes ctx cancel
		// - ReadFromUDP aborts otherwise.
		if ctx.Err() != nil || fatalErr != nil {
			return
		}

		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			fatalErr = err
			continue
		}
		msg := new(dns.Msg)
		if err := msg.Unpack(buf[:n]); err != nil {
			log.Printf("[WARN] mdns: [%s] Failed to unpack unicast packet: %v", src, err)
			continue
		}
		dMsg := &dnsMsg{msg: msg, src: src}
		select {
		case msgCh <- dMsg:
			//fmt.Println(msg)
			// Submit decoded DNS message and continue.
		case <-ctx.Done():
			// Abort.
			return
		}
	}
}

// periodicQuery sens multiple probes until a valid response is received by
// the main processing loop or some timeout/cancel fires.
// TODO: move error reporting to shutdown function as periodicQuery is called from
// go routine context.
func (c *client) periodicQuery(ctx context.Context, params *lookupParams) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 4 * time.Second
	bo.MaxInterval = 60 * time.Second
	bo.MaxElapsedTime = 0
	bo.Reset()

	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		// Backoff and cancel logic.
		wait := bo.NextBackOff()
		if wait == backoff.Stop {
			return fmt.Errorf("periodicQuery: abort due to timeout")
		}
		if timer == nil {
			timer = time.NewTimer(wait)
		} else {
			timer.Reset(wait)
		}
		select {
		case <-timer.C:
			// Wait for next iteration.
		case <-params.stopProbing:
			// Chan is closed (or happened in the past).
			// Done here. Received a matching mDNS entry.
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
		// Do periodic query.
		if err := c.query(params); err != nil {
			return err
		}
	}
}

// Performs the actual query by service name (browse) or service instance name (lookup),
// start response listeners goroutines and loops over the entries channel.
func (c *client) query(params *lookupParams) error {
	var serviceName, serviceInstanceName string
	serviceName = fmt.Sprintf("%s.%s.", trimDot(params.Service), trimDot(params.Domain))

	// send the query
	m := new(dns.Msg)
	if params.Instance != "" { // service instance name lookup
		serviceInstanceName = fmt.Sprintf("%s.%s", params.Instance, serviceName)
		m.Question = []dns.Question{
			{Name: serviceInstanceName, Qtype: dns.TypeSRV, Qclass: dns.ClassINET},
			{Name: serviceInstanceName, Qtype: dns.TypeTXT, Qclass: dns.ClassINET},
		}
	} else if len(params.Subtypes) > 0 { // service subtype browse
		m.SetQuestion(params.Subtypes[0], dns.TypePTR)
	} else { // service name browse
		m.SetQuestion(serviceName, dns.TypePTR)
	}
	m.RecursionDesired = false
	if err := c.sendQuery(m); err != nil {
		return err
	}

	return nil
}

// Pack the dns.Msg and write to available connections (multicast)
func (c *client) sendQuery(msg *dns.Msg) error {
	buf, err := msg.Pack()
	if err != nil {
		return err
	}
	if c.ipv4conn != nil {
		// See https://pkg.go.dev/golang.org/x/net/ipv4#pkg-note-BUG
		// As of Golang 1.18.4
		// On Windows, the ControlMessage for ReadFrom and WriteTo methods of PacketConn is not implemented.
		var wcm ipv4.ControlMessage
		for ifi := range c.ifaces {
			switch runtime.GOOS {
			case "darwin", "ios", "linux":
				wcm.IfIndex = c.ifaces[ifi].Index
			default:
				if err := c.ipv4conn.SetMulticastInterface(&c.ifaces[ifi]); err != nil {
					log.Printf("[WARN] mdns: Failed to set multicast interface: %s error: %v", c.ifaces[ifi].Name, err)
				}
			}
			c.ipv4conn.WriteTo(buf, &wcm, ipv4Addr)
		}
	}
	if c.ipv6conn != nil {
		// See https://pkg.go.dev/golang.org/x/net/ipv6#pkg-note-BUG
		// As of Golang 1.18.4
		// On Windows, the ControlMessage for ReadFrom and WriteTo methods of PacketConn is not implemented.
		var wcm ipv6.ControlMessage
		for ifi := range c.ifaces {
			switch runtime.GOOS {
			case "darwin", "ios", "linux":
				wcm.IfIndex = c.ifaces[ifi].Index
			default:
				if err := c.ipv6conn.SetMulticastInterface(&c.ifaces[ifi]); err != nil {
					log.Printf("[WARN] mdns: Failed to set multicast interface: %s error: %v", c.ifaces[ifi].Name, err)
				}
			}
			c.ipv6conn.WriteTo(buf, &wcm, ipv6Addr)
		}
	}
	return nil
}
