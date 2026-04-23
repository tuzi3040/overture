package outbound

import (
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/shawn1m/overture/core/common"
	"github.com/shawn1m/overture/core/config"
	"github.com/shawn1m/overture/core/outbound/clients/resolver"
)

var dispatcher Dispatcher
var questionDomain = "www.yahoo.com."

type staticResolver struct {
	ip string
}

func (r staticResolver) Init() error { return nil }

func (r staticResolver) Exchange(q *dns.Msg) (*dns.Msg, error) {
	resp := new(dns.Msg)
	resp.SetReply(q)
	resp.Answer = []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{
				Name:   q.Question[0].Name,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    60,
			},
			A: net.ParseIP(r.ip),
		},
	}
	return resp, nil
}

func init() {
	os.Chdir("../..")
	conf := config.NewConfig("config.test.yml")
	dispatcher = Dispatcher{
		PrimaryDNS:                  conf.PrimaryDNS,
		AlternativeDNS:              conf.AlternativeDNS,
		OnlyPrimaryDNS:              conf.OnlyPrimaryDNS,
		WhenPrimaryDNSAnswerNoneUse: conf.WhenPrimaryDNSAnswerNoneUse,
		IPNetworkPrimarySet:         conf.IPNetworkPrimarySet,
		IPNetworkAlternativeSet:     conf.IPNetworkAlternativeSet,
		DomainPrimaryList:           conf.DomainPrimaryList,
		DomainAlternativeList:       conf.DomainAlternativeList,

		RedirectIPv6Record:       conf.IPv6UseAlternativeDNS,
		AlternativeDNSConcurrent: conf.AlternativeDNSConcurrent,
		MinimumTTL:               conf.MinimumTTL,
		DomainTTLMap:             conf.DomainTTLMap,

		Hosts: conf.Hosts,
		Cache: conf.Cache,
	}
	dispatcher.Init()
}

func TestDispatcher(t *testing.T) {

	testA(t)
	testAAAA(t)
	testHosts(t)
	testIPResponse(t)
	testCache(t)
}

func TestConcurrentAlternativeDoesNotLeakGoroutines(t *testing.T) {
	_, primaryNet, err := net.ParseCIDR("1.2.3.0/24")
	if err != nil {
		t.Fatal(err)
	}
	_, alternativeNet, err := net.ParseCIDR("5.6.7.0/24")
	if err != nil {
		t.Fatal(err)
	}

	upstream := func(name string) *common.DNSUpstream {
		return &common.DNSUpstream{
			Name:     name,
			Protocol: "udp",
			EDNSClientSubnet: &common.EDNSClientSubnetType{
				Policy: "disable",
			},
		}
	}

	d := Dispatcher{
		PrimaryDNS:               []*common.DNSUpstream{upstream("primary")},
		AlternativeDNS:           []*common.DNSUpstream{upstream("alternative")},
		AlternativeDNSConcurrent: true,
		IPNetworkPrimarySet:      common.NewIPSet([]*net.IPNet{primaryNet}),
		IPNetworkAlternativeSet:  common.NewIPSet([]*net.IPNet{alternativeNet}),
		primaryResolvers:         []resolver.Resolver{staticResolver{ip: "1.2.3.4"}},
		alternativeResolvers:     []resolver.Resolver{staticResolver{ip: "5.6.7.8"}},
	}

	before := runtime.NumGoroutine()
	for i := 0; i < 25; i++ {
		q := new(dns.Msg)
		q.SetQuestion(questionDomain, dns.TypeA)
		resp := d.Exchange(q, "")
		if got := common.FindRecordByType(resp, dns.TypeA); got != "1.2.3.4" {
			t.Fatalf("expected primary response, got %s", got)
		}
	}
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()

	if after > before+5 {
		t.Fatalf("possible goroutine leak: before=%d after=%d", before, after)
	}
}

func testA(t *testing.T) {

	resp := exchange(questionDomain, dns.TypeA)
	if net.ParseIP(common.FindRecordByType(resp, dns.TypeA)).To4() == nil {
		t.Error(questionDomain + " should have A record")
	}
}

func testAAAA(t *testing.T) {

	resp := exchange(questionDomain, dns.TypeAAAA)
	if net.ParseIP(common.FindRecordByType(resp, dns.TypeAAAA)).To16() == nil {
		t.Error(questionDomain + " should have AAAA record")
	}
}

func testHosts(t *testing.T) {

	resp := exchange("localhost.", dns.TypeA)
	if common.FindRecordByType(resp, dns.TypeA) != "127.0.0.1" {
		t.Error("localhost should be 127.0.0.1")
	}
}

func testIPResponse(t *testing.T) {

	resp := exchange("127.0.0.1.", dns.TypeA)
	if common.FindRecordByType(resp, dns.TypeA) != "127.0.0.1" {
		t.Error("127.0.0.1 should be 127.0.0.1")
	}

	resp = exchange("fe80::7f:4f42:3f4d:f4c8.", dns.TypeAAAA)
	if common.FindRecordByType(resp, dns.TypeAAAA) != "fe80::7f:4f42:3f4d:f4c8" {
		t.Error("fe80::7f:4f42:3f4d:f4c8 should be fe80::7f:4f42:3f4d:f4c8")
	}
}

func testCache(t *testing.T) {

	exchange(questionDomain, dns.TypeA)
	now := time.Now()
	exchange(questionDomain, dns.TypeA)
	if time.Since(now) > 10*time.Millisecond {
		t.Error(time.Since(now).String() + " " + "Cache response slower than 10ms")
	}
}

func exchange(z string, t uint16) *dns.Msg {

	q := new(dns.Msg)
	q.SetQuestion(z, t)
	return dispatcher.Exchange(q, "")
}
