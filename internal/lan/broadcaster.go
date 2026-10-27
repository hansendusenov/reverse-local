package lan

import (
	"encoding/binary"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	mdnsMulticastAddr = "224.0.0.251:5353"
	dnsTypeA          = 1
	dnsTypeAAAA       = 28
	dnsTypeANY        = 255
	dnsClassIN        = 1
)

// DomainMatcher reports whether a domain name is managed by Reverse.Local.
type DomainMatcher func(domain string) bool

// Broadcaster runs a lightweight mDNS (UDP :5353) and LAN DNS (UDP :53) responder
// so devices on the same Wi-Fi/LAN can resolve Reverse.Local domains to the host's LAN IPv4.
type Broadcaster struct {
	mu              sync.RWMutex
	matcher         DomainMatcher
	dnsPort         int
	lanIP           string
	running         bool
	mdnsConns       []*net.UDPConn
	dnsConn         *net.UDPConn
	stopCh          chan struct{}
	queriesAnswered uint64
}

// NewBroadcaster creates a new LAN DNS & mDNS broadcaster.
func NewBroadcaster(matcher DomainMatcher, dnsPort int) *Broadcaster {
	if dnsPort <= 0 || dnsPort > 65535 {
		dnsPort = 53
	}
	return &Broadcaster{
		matcher: matcher,
		dnsPort: dnsPort,
		lanIP:   DetectLANIP(),
	}
}

// DetectLANIP discovers the machine's primary outbound LAN IPv4 address (e.g., 192.168.1.x).
func DetectLANIP() string {
	conn, err := net.DialTimeout("udp4", "8.8.8.8:80", 500*time.Millisecond)
	if err == nil {
		defer conn.Close()
		if udpAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok && udpAddr.IP != nil && !udpAddr.IP.IsLoopback() {
			if ip4 := udpAddr.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}

	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
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
				if ip == nil || ip.IsLoopback() {
					continue
				}
				if ip4 := ip.To4(); ip4 != nil && !ip4.IsLinkLocalUnicast() {
					return ip4.String()
				}
			}
		}
	}

	return "127.0.0.1"
}

// Start begins listening on mDNS (UDP 224.0.0.251:5353) and optionally LAN DNS (UDP :dnsPort).
func (b *Broadcaster) Start(enableDNSServer bool, dnsPort int) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stopLocked()
	if dnsPort > 0 && dnsPort <= 65535 {
		b.dnsPort = dnsPort
	}
	b.lanIP = DetectLANIP()
	b.stopCh = make(chan struct{})

	mcastAddr, err := net.ResolveUDPAddr("udp4", mdnsMulticastAddr)
	if err == nil {
		// Bind multicast listener on default interface
		if conn, err := net.ListenMulticastUDP("udp4", nil, mcastAddr); err == nil {
			_ = conn.SetReadBuffer(65535)
			b.mdnsConns = append(b.mdnsConns, conn)
			go b.serveUDP(conn, true, b.stopCh)
		}

		// Also bind on active multicast-capable LAN interfaces for multi-NIC Windows/macOS machines
		if ifaces, iErr := net.Interfaces(); iErr == nil {
			for _, iface := range ifaces {
				if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
					continue
				}
				ifaceCopy := iface
				if conn, err := net.ListenMulticastUDP("udp4", &ifaceCopy, mcastAddr); err == nil {
					_ = conn.SetReadBuffer(65535)
					b.mdnsConns = append(b.mdnsConns, conn)
					go b.serveUDP(conn, true, b.stopCh)
				}
			}
		}
	}

	if enableDNSServer {
		dnsAddr := &net.UDPAddr{IP: net.IPv4zero, Port: b.dnsPort}
		conn, err := net.ListenUDP("udp4", dnsAddr)
		if err != nil {
			log.Printf("[WARN] LAN DNS UDP :%d could not bind (mDNS :5353 still active): %v", b.dnsPort, err)
		} else {
			b.dnsConn = conn
			go b.serveUDP(conn, false, b.stopCh)
			log.Printf("[INFO] LAN DNS Responder listening on UDP 0.0.0.0:%d -> %s", b.dnsPort, b.lanIP)
		}
	}

	b.running = len(b.mdnsConns) > 0 || b.dnsConn != nil
	if len(b.mdnsConns) > 0 {
		log.Printf("[INFO] LAN mDNS Responder active on %s -> %s", mdnsMulticastAddr, b.lanIP)
	}
	return nil
}

// Stop halts all mDNS and LAN DNS UDP listeners.
func (b *Broadcaster) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopLocked()
}

func (b *Broadcaster) stopLocked() {
	if b.stopCh != nil {
		close(b.stopCh)
		b.stopCh = nil
	}
	for _, c := range b.mdnsConns {
		_ = c.Close()
	}
	b.mdnsConns = nil
	if b.dnsConn != nil {
		_ = b.dnsConn.Close()
		b.dnsConn = nil
	}
	b.running = false
}

// IsRunning returns true if mDNS or LAN DNS listeners are active.
func (b *Broadcaster) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.running
}

// LANIP returns the current detected LAN IPv4 address.
func (b *Broadcaster) LANIP() string {
	b.mu.RLock()
	ip := b.lanIP
	b.mu.RUnlock()
	if ip == "" || ip == "127.0.0.1" {
		ip = DetectLANIP()
	}
	return ip
}

// QueriesAnswered returns the total number of LAN DNS/mDNS queries resolved by this daemon.
func (b *Broadcaster) QueriesAnswered() uint64 {
	return atomic.LoadUint64(&b.queriesAnswered)
}

func (b *Broadcaster) serveUDP(conn *net.UDPConn, isMDNS bool, stopCh <-chan struct{}) {
	buf := make([]byte, 1500)
	mcastDest, _ := net.ResolveUDPAddr("udp4", mdnsMulticastAddr)

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			select {
			case <-stopCh:
				return
			default:
				continue
			}
		}

		packet := make([]byte, n)
		copy(packet, buf[:n])

		q, err := ParseDNSQuery(packet)
		if err != nil {
			continue
		}

		ipStr := b.LANIP()
		ip4 := net.ParseIP(ipStr).To4()
		if ip4 == nil {
			continue
		}

		if b.matchesDomain(q.Domain) {
			resp, err := BuildDNSResponse(packet, q, ip4, 60)
			if err == nil && len(resp) > 0 {
				atomic.AddUint64(&b.queriesAnswered, 1)
				_, _ = conn.WriteToUDP(resp, remoteAddr)
				if isMDNS && mcastDest != nil && !q.UnicastResponse {
					_, _ = conn.WriteToUDP(resp, mcastDest)
				}
			}
			continue
		}

		// If standard unicast DNS server (not mDNS), forward non-local domains to upstream DNS
		if !isMDNS {
			go forwardUpstreamDNS(conn, remoteAddr, packet)
		}
	}
}

func (b *Broadcaster) matchesDomain(domain string) bool {
	clean := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if clean == "" {
		return false
	}
	if clean == "reverse.local" {
		return true
	}
	if b.matcher != nil {
		return b.matcher(clean)
	}
	return false
}

func forwardUpstreamDNS(listener *net.UDPConn, clientAddr *net.UDPAddr, query []byte) {
	upstreams := []string{"8.8.8.8:53", "1.1.1.1:53"}
	for _, upstream := range upstreams {
		uConn, err := net.DialTimeout("udp4", upstream, 1500*time.Millisecond)
		if err != nil {
			continue
		}
		_ = uConn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := uConn.Write(query); err != nil {
			_ = uConn.Close()
			continue
		}
		resp := make([]byte, 1500)
		n, err := uConn.Read(resp)
		_ = uConn.Close()
		if err == nil && n > 0 {
			_, _ = listener.WriteToUDP(resp[:n], clientAddr)
			return
		}
	}
}

// DNSQuery represents a parsed DNS question header and first question entry.
type DNSQuery struct {
	TransactionID   uint16
	Domain          string
	QType           uint16
	QClass          uint16
	UnicastResponse bool
	QuestionEndIdx  int
}

// ParseDNSQuery parses the first DNS question from a raw UDP DNS/mDNS packet.
func ParseDNSQuery(packet []byte) (DNSQuery, error) {
	if len(packet) < 12 {
		return DNSQuery{}, errors.New("packet too short for DNS header")
	}

	flags := binary.BigEndian.Uint16(packet[2:4])
	// QR bit (0x8000) must be 0 (Query)
	if flags&0x8000 != 0 {
		return DNSQuery{}, errors.New("not a DNS query packet")
	}

	qdCount := binary.BigEndian.Uint16(packet[4:6])
	if qdCount == 0 {
		return DNSQuery{}, errors.New("no questions in DNS packet")
	}

	idx := 12
	var labels []string
	for {
		if idx >= len(packet) {
			return DNSQuery{}, errors.New("truncated QNAME")
		}
		length := int(packet[idx])
		idx++
		if length == 0 {
			break
		}
		// Reject compressed pointers in first question QNAME for simplicity/safety
		if length&0xC0 != 0 {
			return DNSQuery{}, errors.New("compressed QNAME in first question unsupported")
		}
		if idx+length > len(packet) {
			return DNSQuery{}, errors.New("label exceeds packet bounds")
		}
		labels = append(labels, string(packet[idx:idx+length]))
		idx += length
	}

	if idx+4 > len(packet) {
		return DNSQuery{}, errors.New("truncated QTYPE/QCLASS")
	}

	qType := binary.BigEndian.Uint16(packet[idx : idx+2])
	rawClass := binary.BigEndian.Uint16(packet[idx+2 : idx+4])
	unicast := (rawClass & 0x8000) != 0
	qClass := rawClass & 0x7FFF

	return DNSQuery{
		TransactionID:   binary.BigEndian.Uint16(packet[0:2]),
		Domain:          strings.ToLower(strings.Join(labels, ".")),
		QType:           qType,
		QClass:          qClass,
		UnicastResponse: unicast,
		QuestionEndIdx:  idx + 4,
	}, nil
}

// BuildDNSResponse constructs an authoritative DNS response for a matched domain.
// For A (1) or ANY (255) queries, it returns an A record with ipv4.
// For other record types (such as AAAA), it returns an empty authoritative NOERROR response
// so dual-stack clients immediately proceed with the IPv4 A record.
func BuildDNSResponse(queryPacket []byte, q DNSQuery, ipv4 net.IP, ttlSec uint32) ([]byte, error) {
	ip4 := ipv4.To4()
	if ip4 == nil {
		return nil, errors.New("valid IPv4 address required")
	}
	if q.QuestionEndIdx > len(queryPacket) || q.QuestionEndIdx < 12 {
		return nil, errors.New("invalid question slice")
	}

	questionSection := queryPacket[12:q.QuestionEndIdx]
	includeARecord := q.QType == dnsTypeA || q.QType == dnsTypeANY

	resp := make([]byte, 12, 12+len(questionSection)+16)
	binary.BigEndian.PutUint16(resp[0:2], q.TransactionID)
	// Flags: 0x8400 = QR(1) + AA(1, Authoritative Answer) + RCODE(0, NoError)
	binary.BigEndian.PutUint16(resp[2:4], 0x8400)
	binary.BigEndian.PutUint16(resp[4:6], 1) // QDCOUNT = 1
	if includeARecord {
		binary.BigEndian.PutUint16(resp[6:8], 1) // ANCOUNT = 1
	} else {
		binary.BigEndian.PutUint16(resp[6:8], 0) // ANCOUNT = 0 (NODATA / NOERROR)
	}
	binary.BigEndian.PutUint16(resp[8:10], 0)  // NSCOUNT = 0
	binary.BigEndian.PutUint16(resp[10:12], 0) // ARCOUNT = 0

	resp = append(resp, questionSection...)

	if includeARecord {
		// Name pointer to offset 12 (0xC00C)
		ans := make([]byte, 16)
		binary.BigEndian.PutUint16(ans[0:2], 0xC00C)
		binary.BigEndian.PutUint16(ans[2:4], dnsTypeA)
		binary.BigEndian.PutUint16(ans[4:6], dnsClassIN)
		binary.BigEndian.PutUint32(ans[6:10], ttlSec)
		binary.BigEndian.PutUint16(ans[10:12], 4) // RDLENGTH = 4 bytes
		copy(ans[12:16], ip4)
		resp = append(resp, ans...)
	}

	return resp, nil
}

