package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/palmar/mikrotik-traffic-monitor/internal/config"
	"github.com/palmar/mikrotik-traffic-monitor/internal/report"
	"github.com/palmar/mikrotik-traffic-monitor/internal/ringbuf"
	"github.com/palmar/mikrotik-traffic-monitor/internal/server"
	"github.com/palmar/mikrotik-traffic-monitor/internal/snmp"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	srv := server.New()
	done := make(chan struct{})
	var pollers []*snmp.Poller

	for _, dev := range cfg.Devices {
		snmpCfg := snmp.Config{
			Name:         dev.Name,
			Host:         dev.Host,
			Port:         dev.Port,
			SNMPVersion:  dev.SNMPVersion,
			Community:    dev.Community,
			Username:     dev.Username,
			AuthPass:     dev.AuthPass,
			PrivPass:     dev.PrivPass,
			AuthProtocol: dev.AuthProtocol,
			PrivProtocol: dev.PrivProtocol,
			PollInterval: cfg.PollInterval.Duration(),
		}

		poller, discovered, err := snmp.NewPoller(snmpCfg, cfg.RingBufferSize, srv.Broadcast)
		if err != nil {
			log.Printf("WARNING: skipping device %s (unreachable at startup): %v", dev.Name, err)
			continue
		}

		// Collect interface names and buffers for server registration
		var ifaceNames []string
		buffers := make(map[string]*ringbuf.RingBuffer)
		for _, di := range discovered {
			ifaceNames = append(ifaceNames, di.Name)
			buffers[server.BufKey(dev.Name, di.Name)] = di.Buffer
		}
		sort.Strings(ifaceNames)

		srv.RegisterDevice(dev.Name, ifaceNames, buffers, poller)
		pollers = append(pollers, poller)
		log.Printf("device %q: %d interfaces discovered", dev.Name, len(discovered))
	}

	for _, p := range pollers {
		go p.Run(done)
	}

	// Start weekly email report scheduler if configured
	if cfg.Report.ResendAPIKey != "" {
		var deviceEntries []report.DeviceEntry
		for _, dev := range cfg.Devices {
			if dev.OwnerEmail != "" {
				deviceEntries = append(deviceEntries, report.DeviceEntry{
					Name:       dev.Name,
					OwnerEmail: dev.OwnerEmail,
				})
			}
		}
		if len(deviceEntries) > 0 {
			reportCfg := report.Config{
				ResendAPIKey: cfg.Report.ResendAPIKey,
				FromAddr:     cfg.Report.FromAddr,
				Timezone:     cfg.Report.Timezone,
				DayOfWeek:    time.Weekday(cfg.Report.DayOfWeek),
				Hour:         cfg.Report.Hour,
			}
			scheduler, err := report.NewScheduler(reportCfg, deviceEntries, srv.GetBuffer, srv.DeviceInterfaces)
			if err != nil {
				log.Fatalf("report scheduler: %v", err)
			}
			go scheduler.Run(done)
		} else {
			log.Println("report: no devices have owner_email set, skipping report scheduler")
		}
	}

	httpSrv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("listening on %s (%d devices)", cfg.ListenAddr, len(cfg.Devices))
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
	close(done)
	httpSrv.Close()
}
