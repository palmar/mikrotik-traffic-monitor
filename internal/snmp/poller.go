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
	oidIfDescr      = ".1.3.6.1.2.1.2.2.1.2"
	oidIfHCInOctets = ".1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOutOctets = ".1.3.6.1.2.1.31.1.1.1.10"
)

// Config holds SNMP connection settings.
type Config struct {
	Host         string
	Port         uint16
	Username     string
	AuthPass     string
	PrivPass     string
	InterfaceStr string
	PollInterval time.Duration
}

// OnSample is called after each new sample is pushed to the buffer.
type OnSample func(ringbuf.Sample)

// Poller polls SNMP counters and writes samples to a ring buffer.
type Poller struct {
	cfg      Config
	buf      *ringbuf.RingBuffer
	client   *gosnmp.GoSNMP
	onSample OnSample
	ifIndex  int

	prevIn      uint64
	prevOut     uint64
	prevTime    time.Time
	hasBaseline bool
}

// NewPoller creates and connects an SNMP poller.
func NewPoller(cfg Config, buf *ringbuf.RingBuffer, onSample OnSample) (*Poller, error) {
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

	p := &Poller{cfg: cfg, buf: buf, client: client, onSample: onSample}
	if err := p.resolveIfIndex(); err != nil {
		client.Conn.Close()
		return nil, err
	}
	log.Printf("resolved interface %q to ifIndex %d", cfg.InterfaceStr, p.ifIndex)
	return p, nil
}

func (p *Poller) resolveIfIndex() error {
	err := p.client.Walk(oidIfDescr, func(pdu gosnmp.SnmpPDU) error {
		name, ok := pdu.Value.([]byte)
		if !ok {
			return nil
		}
		if strings.EqualFold(string(name), p.cfg.InterfaceStr) {
			// OID is .1.3.6.1.2.1.2.2.1.2.<ifIndex>
			parts := strings.Split(pdu.Name, ".")
			if len(parts) > 0 {
				fmt.Sscanf(parts[len(parts)-1], "%d", &p.ifIndex)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk ifDescr: %w", err)
	}
	if p.ifIndex == 0 {
		return fmt.Errorf("interface %q not found via SNMP", p.cfg.InterfaceStr)
	}
	return nil
}

func (p *Poller) poll() error {
	inOID := fmt.Sprintf("%s.%d", oidIfHCInOctets, p.ifIndex)
	outOID := fmt.Sprintf("%s.%d", oidIfHCOutOctets, p.ifIndex)

	result, err := p.client.Get([]string{inOID, outOID})
	if err != nil {
		return fmt.Errorf("snmp get: %w", err)
	}

	now := time.Now()
	var inOctets, outOctets uint64
	for _, v := range result.Variables {
		switch v.Name {
		case inOID:
			inOctets = gosnmp.ToBigInt(v.Value).Uint64()
		case outOID:
			outOctets = gosnmp.ToBigInt(v.Value).Uint64()
		}
	}

	if p.hasBaseline {
		dt := now.Sub(p.prevTime).Seconds()
		if dt > 0 {
			inDelta := counterDelta(p.prevIn, inOctets)
			outDelta := counterDelta(p.prevOut, outOctets)
			s := ringbuf.Sample{
				Timestamp: now.Unix(),
				InBps:     float64(inDelta) * 8 / dt,
				OutBps:    float64(outDelta) * 8 / dt,
			}
			p.buf.Push(s)
			if p.onSample != nil {
				p.onSample(s)
			}
		}
	}

	p.prevIn = inOctets
	p.prevOut = outOctets
	p.prevTime = now
	p.hasBaseline = true
	return nil
}

func counterDelta(prev, curr uint64) uint64 {
	if curr >= prev {
		return curr - prev
	}
	// Counter wrap
	return math.MaxUint64 - prev + curr + 1
}

// Run starts the poll loop. Blocks until done is closed.
func (p *Poller) Run(done <-chan struct{}) {
	// Initial poll to establish baseline
	if err := p.poll(); err != nil {
		log.Printf("initial poll error: %v", err)
	}

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	defer p.client.Conn.Close()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := p.poll(); err != nil {
				log.Printf("poll error: %v", err)
			}
		}
	}
}
