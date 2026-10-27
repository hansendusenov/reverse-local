package lan

import (
	"encoding/binary"
	"net"
	"testing"
)

func buildTestDNSQuery(txID uint16, domain string, qType uint16) []byte {
	pkt := make([]byte, 12)
	binary.BigEndian.PutUint16(pkt[0:2], txID)
	binary.BigEndian.PutUint16(pkt[2:4], 0x0100) // Standard query
	binary.BigEndian.PutUint16(pkt[4:6], 1)      // QDCOUNT = 1

	for _, label := range []string{"api", "project", "local"} {
		if domain == "reverse.local" {
			break
		}
		pkt = append(pkt, byte(len(label)))
		pkt = append(pkt, []byte(label)...)
	}
	if domain == "reverse.local" {
		for _, label := range []string{"reverse", "local"} {
			pkt = append(pkt, byte(len(label)))
			pkt = append(pkt, []byte(label)...)
		}
	}
	pkt = append(pkt, 0x00) // Root label

	tail := make([]byte, 4)
	binary.BigEndian.PutUint16(tail[0:2], qType)
	binary.BigEndian.PutUint16(tail[2:4], dnsClassIN)
	return append(pkt, tail...)
}

func TestParseAndBuildDNSResponse(t *testing.T) {
	raw := buildTestDNSQuery(0xBEEF, "api.project.local", dnsTypeA)

	q, err := ParseDNSQuery(raw)
	if err != nil {
		t.Fatalf("ParseDNSQuery failed: %v", err)
	}
	if q.TransactionID != 0xBEEF {
		t.Fatalf("expected txID 0xBEEF, got 0x%X", q.TransactionID)
	}
	if q.Domain != "api.project.local" {
		t.Fatalf("expected domain api.project.local, got %s", q.Domain)
	}
	if q.QType != dnsTypeA {
		t.Fatalf("expected QType A (1), got %d", q.QType)
	}

	targetIP := net.ParseIP("192.168.1.50")
	resp, err := BuildDNSResponse(raw, q, targetIP, 60)
	if err != nil {
		t.Fatalf("BuildDNSResponse failed: %v", err)
	}

	// Verify ANCOUNT = 1 and trailing 4 bytes match 192.168.1.50
	anCount := binary.BigEndian.Uint16(resp[6:8])
	if anCount != 1 {
		t.Fatalf("expected ANCOUNT 1, got %d", anCount)
	}
	gotIP := net.IP(resp[len(resp)-4:])
	if !gotIP.Equal(targetIP.To4()) {
		t.Fatalf("expected response IP %s, got %s", targetIP, gotIP)
	}
}

func TestBuildDNSResponseAAAAReturnsNoData(t *testing.T) {
	raw := buildTestDNSQuery(0x1234, "reverse.local", dnsTypeAAAA)
	q, err := ParseDNSQuery(raw)
	if err != nil {
		t.Fatalf("ParseDNSQuery failed: %v", err)
	}

	resp, err := BuildDNSResponse(raw, q, net.ParseIP("192.168.1.50"), 60)
	if err != nil {
		t.Fatalf("BuildDNSResponse failed: %v", err)
	}
	anCount := binary.BigEndian.Uint16(resp[6:8])
	if anCount != 0 {
		t.Fatalf("expected ANCOUNT 0 for AAAA query, got %d", anCount)
	}
}

func TestDetectLANIP(t *testing.T) {
	ip := DetectLANIP()
	if net.ParseIP(ip) == nil {
		t.Fatalf("expected valid IP from DetectLANIP, got %q", ip)
	}
}

