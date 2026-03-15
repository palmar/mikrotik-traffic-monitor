package snmp

import (
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/palmar/mikrotik-traffic-monitor/internal/ringbuf"
)

const (
	oidIfDescr       = ".1.3.6.1.2.1.2.2.1.2"
	oidIfHCInOctets  = ".1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOutOctets = ".1.3.6.1.2.1.31.1.1.1.10"
)

// Config holds SNMP connection settings for a single device.
type Config struct {
	Name         string
	Host         string
	Port         uint16
	SNMPVersion  string // "v2c" or "v3"
	Community    string // SNMPv2c community string
	Username     string
	AuthPass     string
	PrivPass     string
	AuthProtocol string // "sha1" or "sha256"
	PrivProtocol string // "aes" or "des"
	PollInterval time.Duration
}

// InterfaceSample is emitted after each poll for a specific interface.
type InterfaceSample struct {
	Device    string
	Interface string
	Sample    ringbuf.Sample
}

// OnSample is called after each new sample is pushed to a buffer.
type OnSample func(InterfaceSample)

// ifState tracks per-interface SNMP counter state.
type ifState struct {
	name        string
	ifIndex     int
	buf         *ringbuf.RingBuffer
	prevIn      uint64
	prevOut     uint64
	prevTime    time.Time
	hasBaseline bool
}

// Poller polls SNMP counters for all interfaces on a single device.
type Poller struct {
	cfg        Config
	client     *gosnmp.GoSNMP
	onSample   OnSample
	bufSize    int
	mu         sync.Mutex
	interfaces []*ifState
}

// NewPoller creates and connects an SNMP poller for a device.
// It auto-discovers all interfaces and creates ring buffers for each.
func NewPoller(cfg Config, bufSize int, onSample OnSample) (*Poller, []*DiscoveredInterface, error) {
	client := &gosnmp.GoSNMP{
		Target:  cfg.Host,
		Port:    cfg.Port,
		Timeout: 5 * time.Second,
	}

	switch cfg.SNMPVersion {
	case "v2c":
		client.Version = gosnmp.Version2c
		client.Community = cfg.Community
	default: // v3
		client.Version = gosnmp.Version3
		client.SecurityModel = gosnmp.UserSecurityModel
		client.MsgFlags = gosnmp.AuthPriv

		authProto := gosnmp.SHA
		if cfg.AuthProtocol == "sha256" {
			authProto = gosnmp.SHA256
		}
		privProto := gosnmp.AES
		if cfg.PrivProtocol == "des" {
			privProto = gosnmp.DES
		}

		client.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 cfg.Username,
			AuthenticationProtocol:   authProto,
			AuthenticationPassphrase: cfg.AuthPass,
			PrivacyProtocol:          privProto,
			PrivacyPassphrase:        cfg.PrivPass,
		}
	}

	if err := client.Connect(); err != nil {
		return nil, nil, fmt.Errorf("snmp connect %s: %w", cfg.Name, err)
	}

	p := &Poller{cfg: cfg, client: client, onSample: onSample, bufSize: bufSize}

	ifIndexMap, err := p.walkInterfaces()
	if err != nil {
		client.Conn.Close()
		return nil, nil, err
	}

	var discovered []*DiscoveredInterface
	for name, idx := range ifIndexMap {
		buf := ringbuf.New(bufSize)
		p.interfaces = append(p.interfaces, &ifState{
			name:    name,
			ifIndex: idx,
			buf:     buf,
		})
		discovered = append(discovered, &DiscoveredInterface{
			Name:    name,
			IfIndex: idx,
			Buffer:  buf,
		})
		log.Printf("[%s] discovered interface %q (ifIndex %d)", cfg.Name, name, idx)
	}

	return p, discovered, nil
}

// DiscoveredInterface holds info about an auto-discovered interface.
type DiscoveredInterface struct {
	Name    string
	IfIndex int
	Buffer  *ringbuf.RingBuffer
}

// walkInterfaces returns a map of lowercase interface name -> ifIndex.
func (p *Poller) walkInterfaces() (map[string]int, error) {
	result := make(map[string]int)
	err := p.client.Walk(oidIfDescr, func(pdu gosnmp.SnmpPDU) error {
		name, ok := pdu.Value.([]byte)
		if !ok {
			return nil
		}
		parts := strings.Split(pdu.Name, ".")
		if len(parts) > 0 {
			var idx int
			fmt.Sscanf(parts[len(parts)-1], "%d", &idx)
			if idx > 0 {
				result[strings.ToLower(string(name))] = idx
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk ifDescr on %s: %w", p.cfg.Name, err)
	}
	return result, nil
}

// Rediscover re-queries the device for interfaces and returns any newly found ones.
// Existing interfaces (and their polling state) are preserved.
func (p *Poller) Rediscover() ([]*DiscoveredInterface, error) {
	ifIndexMap, err := p.walkInterfaces()
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Build set of existing interface names
	existing := make(map[string]bool, len(p.interfaces))
	for _, iface := range p.interfaces {
		existing[iface.name] = true
	}

	var newlyDiscovered []*DiscoveredInterface
	for name, idx := range ifIndexMap {
		if existing[name] {
			continue
		}
		buf := ringbuf.New(p.bufSize)
		p.interfaces = append(p.interfaces, &ifState{
			name:    name,
			ifIndex: idx,
			buf:     buf,
		})
		newlyDiscovered = append(newlyDiscovered, &DiscoveredInterface{
			Name:    name,
			IfIndex: idx,
			Buffer:  buf,
		})
		log.Printf("[%s] rediscovered new interface %q (ifIndex %d)", p.cfg.Name, name, idx)
	}

	log.Printf("[%s] rediscovery complete: %d total, %d new", p.cfg.Name, len(p.interfaces), len(newlyDiscovered))
	return newlyDiscovered, nil
}

// InterfaceNames returns the names of all known interfaces.
func (p *Poller) InterfaceNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, len(p.interfaces))
	for i, iface := range p.interfaces {
		names[i] = iface.name
	}
	return names
}

func (p *Poller) poll() {
	now := time.Now()

	p.mu.Lock()
	snapshot := make([]*ifState, len(p.interfaces))
	copy(snapshot, p.interfaces)
	p.mu.Unlock()

	var oids []string
	for _, iface := range snapshot {
		oids = append(oids,
			fmt.Sprintf("%s.%d", oidIfHCInOctets, iface.ifIndex),
			fmt.Sprintf("%s.%d", oidIfHCOutOctets, iface.ifIndex),
		)
	}

	result, err := p.client.Get(oids)
	if err != nil {
		log.Printf("[%s] snmp get: %v", p.cfg.Name, err)
		return
	}

	values := make(map[string]uint64, len(result.Variables))
	for _, v := range result.Variables {
		values[v.Name] = gosnmp.ToBigInt(v.Value).Uint64()
	}

	for _, iface := range snapshot {
		inOID := fmt.Sprintf("%s.%d", oidIfHCInOctets, iface.ifIndex)
		outOID := fmt.Sprintf("%s.%d", oidIfHCOutOctets, iface.ifIndex)
		inOctets := values[inOID]
		outOctets := values[outOID]

		if iface.hasBaseline {
			dt := now.Sub(iface.prevTime).Seconds()
			if dt > 0 {
				inDelta := counterDelta(iface.prevIn, inOctets)
				outDelta := counterDelta(iface.prevOut, outOctets)
				s := ringbuf.Sample{
					Timestamp: now.Unix(),
					InBps:     float64(inDelta) * 8 / dt,
					OutBps:    float64(outDelta) * 8 / dt,
				}
				iface.buf.Push(s)
				if p.onSample != nil {
					p.onSample(InterfaceSample{Device: p.cfg.Name, Interface: iface.name, Sample: s})
				}
			}
		}

		iface.prevIn = inOctets
		iface.prevOut = outOctets
		iface.prevTime = now
		iface.hasBaseline = true
	}
}

func counterDelta(prev, curr uint64) uint64 {
	if curr >= prev {
		return curr - prev
	}
	return math.MaxUint64 - prev + curr + 1
}

// Run starts the poll loop. Blocks until done is closed.
func (p *Poller) Run(done <-chan struct{}) {
	p.poll()

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	defer p.client.Conn.Close()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			p.poll()
		}
	}
}
