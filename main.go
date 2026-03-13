package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/palmar/mikrotik-traffic-monitor/internal/config"
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
			Username:     dev.Username,
			AuthPass:     dev.AuthPass,
			PrivPass:     dev.PrivPass,
			PollInterval: cfg.PollInterval.Duration(),
		}

		poller, discovered, err := snmp.NewPoller(snmpCfg, cfg.RingBufferSize, srv.Broadcast)
		if err != nil {
			log.Fatalf("failed to create poller for %s: %v", dev.Name, err)
		}

		// Collect interface names and buffers for server registration
		var ifaceNames []string
		buffers := make(map[string]*ringbuf.RingBuffer)
		for _, di := range discovered {
			ifaceNames = append(ifaceNames, di.Name)
			buffers[server.BufKey(dev.Name, di.Name)] = di.Buffer
		}
		sort.Strings(ifaceNames)

		srv.RegisterDevice(dev.Name, ifaceNames, buffers)
		pollers = append(pollers, poller)
		log.Printf("device %q: %d interfaces discovered", dev.Name, len(discovered))
	}

	for _, p := range pollers {
		go p.Run(done)
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
