package snmp

import (
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/palmar/mikrotik-traffic-monitor/internal/ringbuf"
)

const (
	oidIfDescr       = ".1.3.6.1.2.1.2.2.1.2"
	oidIfHCInOctets  = ".1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOutOctets = ".1.3.6.1.2.1.31.1.1.1.10"
)

// Config holds SNMP connection settings.
type Config struct {
	Host         string
	Port         uint16
	Username     string
	AuthPass     string
	PrivPass     string
	PollInterval time.Duration
}

// InterfaceSample is emitted after each poll for a specific interface.
type InterfaceSample struct {
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

// Poller polls SNMP counters for multiple interfaces.
type Poller struct {
	cfg        Config
	client     *gosnmp.GoSNMP
	onSample   OnSample
	interfaces []*ifState
}

// NewPoller creates and connects an SNMP poller for the given interfaces.
// Each interface gets its own ring buffer from the provided map.
func NewPoller(cfg Config, buffers map[string]*ringbuf.RingBuffer, onSample OnSample) (*Poller, error) {
	client := &gosnmp.GoSNMP{
		Target:        cfg.Host,
		Port:          cfg.Port,
		Version:       gosnmp.Version3,
		Timeout:       5 * time.Second,
		SecurityModel: gosnmp.UserSecurityModel,
		MsgFlags:      gosnmp.AuthPriv,
		SecurityParameters: &gosnmp.UsmSecurityParameters{
			UserName:                 cfg.Username,
			AuthenticationProtocol:   gosnmp.SHA256,
			AuthenticationPassphrase: cfg.AuthPass,
			PrivacyProtocol:          gosnmp.AES,
			PrivacyPassphrase:        cfg.PrivPass,
		},
	}

	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("snmp connect: %w", err)
	}

	p := &Poller{cfg: cfg, client: client, onSample: onSample}

	// Build ifIndex map from SNMP walk
	ifIndexMap, err := p.walkInterfaces()
	if err != nil {
		client.Conn.Close()
		return nil, err
	}

	for name, buf := range buffers {
		idx, ok := ifIndexMap[strings.ToLower(name)]
		if !ok {
			client.Conn.Close()
			return nil, fmt.Errorf("interface %q not found via SNMP", name)
		}
		p.interfaces = append(p.interfaces, &ifState{
			name:    name,
			ifIndex: idx,
			buf:     buf,
		})
		log.Printf("resolved interface %q to ifIndex %d", name, idx)
	}

	return p, nil
}

// walkInterfaces returns a map of lowercase interface name → ifIndex.
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
		return nil, fmt.Errorf("walk ifDescr: %w", err)
	}
	return result, nil
}

func (p *Poller) poll() {
	now := time.Now()

	// Build OID list for all interfaces
	var oids []string
	for _, iface := range p.interfaces {
		oids = append(oids,
			fmt.Sprintf("%s.%d", oidIfHCInOctets, iface.ifIndex),
			fmt.Sprintf("%s.%d", oidIfHCOutOctets, iface.ifIndex),
		)
	}

	result, err := p.client.Get(oids)
	if err != nil {
		log.Printf("snmp get: %v", err)
		return
	}

	// Parse results into a map of OID → value
	values := make(map[string]uint64, len(result.Variables))
	for _, v := range result.Variables {
		values[v.Name] = gosnmp.ToBigInt(v.Value).Uint64()
	}

	// Process each interface
	for _, iface := range p.interfaces {
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
					p.onSample(InterfaceSample{Interface: iface.name, Sample: s})
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
