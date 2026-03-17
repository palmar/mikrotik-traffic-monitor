package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/palmar/mikrotik-traffic-monitor/internal/ringbuf"
)

// DeviceReport holds the data needed to generate a report for one device.
type DeviceReport struct {
	Name       string
	OwnerEmail string
	Interfaces []InterfaceReport
}

// InterfaceReport summarizes an interface's recent activity.
type InterfaceReport struct {
	Name   string
	Active bool    // has samples in buffer
	AvgIn  float64 // average inbound bps
	AvgOut float64 // average outbound bps
	PeakIn float64 // peak inbound bps
	PeakOut float64 // peak outbound bps
}

// Config holds report scheduler settings.
type Config struct {
	ResendAPIKey string
	FromAddr     string
	Timezone     string
	DayOfWeek    time.Weekday
	Hour         int
}

// BufferProvider returns the ring buffer for a given device/interface key.
type BufferProvider func(device, iface string) *ringbuf.RingBuffer

// DeviceInterfaces returns interface names for a device.
type DeviceInterfacesProvider func(device string) []string

// Scheduler manages weekly report delivery.
type Scheduler struct {
	cfg        Config
	devices    []DeviceEntry
	getBuf     BufferProvider
	getIfaces  DeviceInterfacesProvider
	loc        *time.Location
	lastSentAt time.Time
}

// DeviceEntry tracks a device and its owner email for reporting.
type DeviceEntry struct {
	Name       string
	OwnerEmail string
}

// NewScheduler creates a report scheduler.
func NewScheduler(cfg Config, devices []DeviceEntry, getBuf BufferProvider, getIfaces DeviceInterfacesProvider) (*Scheduler, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", cfg.Timezone, err)
	}
	return &Scheduler{
		cfg:       cfg,
		devices:   devices,
		getBuf:    getBuf,
		getIfaces: getIfaces,
		loc:       loc,
	}, nil
}

// Run starts the scheduler loop. It blocks until done is closed.
func (s *Scheduler) Run(done <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	log.Printf("report scheduler started: sends %s at %02d:00 %s",
		s.cfg.DayOfWeek, s.cfg.Hour, s.cfg.Timezone)

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			s.checkAndSend()
		}
	}
}

func (s *Scheduler) checkAndSend() {
	now := time.Now().In(s.loc)

	if now.Weekday() != s.cfg.DayOfWeek || now.Hour() != s.cfg.Hour {
		return
	}

	// Only send once per scheduled window (avoid re-sending within the same hour)
	windowStart := time.Date(now.Year(), now.Month(), now.Day(), s.cfg.Hour, 0, 0, 0, s.loc)
	if !s.lastSentAt.Before(windowStart) {
		return
	}

	s.lastSentAt = now
	s.sendReports()
}

func (s *Scheduler) sendReports() {
	// Group devices by owner email
	byEmail := make(map[string][]DeviceReport)

	for _, dev := range s.devices {
		if dev.OwnerEmail == "" {
			continue
		}
		ifaces := s.getIfaces(dev.Name)
		var ifReports []InterfaceReport
		for _, ifName := range ifaces {
			buf := s.getBuf(dev.Name, ifName)
			if buf == nil {
				continue
			}
			ifReports = append(ifReports, summarizeInterface(ifName, buf))
		}
		sort.Slice(ifReports, func(i, j int) bool {
			return ifReports[i].Name < ifReports[j].Name
		})
		byEmail[dev.OwnerEmail] = append(byEmail[dev.OwnerEmail], DeviceReport{
			Name:       dev.Name,
			OwnerEmail: dev.OwnerEmail,
			Interfaces: ifReports,
		})
	}

	for email, reports := range byEmail {
		body := buildEmailBody(reports)
		if err := sendViaResend(s.cfg.ResendAPIKey, s.cfg.FromAddr, email, "NetWatch Weekly Health Report", body); err != nil {
			log.Printf("report: failed to send to %s: %v", email, err)
		} else {
			log.Printf("report: sent weekly report to %s (%d devices)", email, len(reports))
		}
	}
}

func summarizeInterface(name string, buf *ringbuf.RingBuffer) InterfaceReport {
	samples := buf.Snapshot()
	r := InterfaceReport{Name: name, Active: len(samples) > 0}

	if len(samples) == 0 {
		return r
	}

	var sumIn, sumOut float64
	for _, s := range samples {
		sumIn += s.InBps
		sumOut += s.OutBps
		if s.InBps > r.PeakIn {
			r.PeakIn = s.InBps
		}
		if s.OutBps > r.PeakOut {
			r.PeakOut = s.OutBps
		}
	}
	r.AvgIn = sumIn / float64(len(samples))
	r.AvgOut = sumOut / float64(len(samples))
	return r
}

func buildEmailBody(reports []DeviceReport) string {
	var b strings.Builder

	b.WriteString("<html><body style=\"font-family: sans-serif; color: #333;\">\n")
	b.WriteString("<h2>NetWatch Weekly Health Report</h2>\n")
	b.WriteString(fmt.Sprintf("<p>Generated: %s</p>\n", time.Now().UTC().Format("2006-01-02 15:04 UTC")))

	for _, dev := range reports {
		b.WriteString(fmt.Sprintf("<h3>%s</h3>\n", dev.Name))

		if len(dev.Interfaces) == 0 {
			b.WriteString("<p>No interfaces discovered.</p>\n")
			continue
		}

		b.WriteString("<table style=\"border-collapse: collapse; width: 100%;\">\n")
		b.WriteString("<tr style=\"background: #f5f5f5;\">")
		b.WriteString("<th style=\"padding: 8px; text-align: left; border-bottom: 1px solid #ddd;\">Interface</th>")
		b.WriteString("<th style=\"padding: 8px; text-align: left; border-bottom: 1px solid #ddd;\">Status</th>")
		b.WriteString("<th style=\"padding: 8px; text-align: right; border-bottom: 1px solid #ddd;\">Avg In</th>")
		b.WriteString("<th style=\"padding: 8px; text-align: right; border-bottom: 1px solid #ddd;\">Avg Out</th>")
		b.WriteString("<th style=\"padding: 8px; text-align: right; border-bottom: 1px solid #ddd;\">Peak In</th>")
		b.WriteString("<th style=\"padding: 8px; text-align: right; border-bottom: 1px solid #ddd;\">Peak Out</th>")
		b.WriteString("</tr>\n")

		for _, iface := range dev.Interfaces {
			status := "Active"
			if !iface.Active {
				status = "No data"
			}
			b.WriteString("<tr>")
			b.WriteString(fmt.Sprintf("<td style=\"padding: 8px; border-bottom: 1px solid #eee;\">%s</td>", iface.Name))
			b.WriteString(fmt.Sprintf("<td style=\"padding: 8px; border-bottom: 1px solid #eee;\">%s</td>", status))
			b.WriteString(fmt.Sprintf("<td style=\"padding: 8px; text-align: right; border-bottom: 1px solid #eee;\">%s</td>", formatBps(iface.AvgIn)))
			b.WriteString(fmt.Sprintf("<td style=\"padding: 8px; text-align: right; border-bottom: 1px solid #eee;\">%s</td>", formatBps(iface.AvgOut)))
			b.WriteString(fmt.Sprintf("<td style=\"padding: 8px; text-align: right; border-bottom: 1px solid #eee;\">%s</td>", formatBps(iface.PeakIn)))
			b.WriteString(fmt.Sprintf("<td style=\"padding: 8px; text-align: right; border-bottom: 1px solid #eee;\">%s</td>", formatBps(iface.PeakOut)))
			b.WriteString("</tr>\n")
		}
		b.WriteString("</table>\n")
	}

	b.WriteString("<p style=\"color: #999; font-size: 12px; margin-top: 24px;\">Sent by NetWatch. Metrics reflect the most recent monitoring window.</p>\n")
	b.WriteString("</body></html>")
	return b.String()
}

func formatBps(bps float64) string {
	switch {
	case bps >= 1_000_000_000:
		return fmt.Sprintf("%.1f Gbps", bps/1_000_000_000)
	case bps >= 1_000_000:
		return fmt.Sprintf("%.1f Mbps", bps/1_000_000)
	case bps >= 1_000:
		return fmt.Sprintf("%.1f Kbps", bps/1_000)
	default:
		return fmt.Sprintf("%.0f bps", bps)
	}
}

func sendViaResend(apiKey, from, to, subject, htmlBody string) error {
	payload := map[string]interface{}{
		"from":    from,
		"to":      []string{to},
		"subject": subject,
		"html":    htmlBody,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var respBody bytes.Buffer
		respBody.ReadFrom(resp.Body)
		return fmt.Errorf("resend API returned %d: %s", resp.StatusCode, respBody.String())
	}
	return nil
}
