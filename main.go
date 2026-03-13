package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
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

	// Create a ring buffer per interface
	buffers := make(map[string]*ringbuf.RingBuffer, len(cfg.Interfaces))
	for _, iface := range cfg.Interfaces {
		buffers[iface] = ringbuf.New(cfg.RingBufferSize)
	}

	srv := server.New(buffers, cfg.Interfaces)

	snmpCfg := snmp.Config{
		Host:         cfg.Router.Host,
		Port:         cfg.Router.Port,
		Username:     cfg.Router.Username,
		AuthPass:     cfg.Router.AuthPass,
		PrivPass:     cfg.Router.PrivPass,
		PollInterval: cfg.PollInterval.Duration(),
	}

	poller, err := snmp.NewPoller(snmpCfg, buffers, srv.Broadcast)
	if err != nil {
		log.Fatalf("failed to create poller: %v", err)
	}

	done := make(chan struct{})
	go poller.Run(done)

	httpSrv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("listening on %s (interfaces: %v)", cfg.ListenAddr, cfg.Interfaces)
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
