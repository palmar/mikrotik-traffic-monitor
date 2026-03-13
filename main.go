package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/palmar/mikrotik-traffic-monitor/internal/ringbuf"
	"github.com/palmar/mikrotik-traffic-monitor/internal/server"
	"github.com/palmar/mikrotik-traffic-monitor/internal/snmp"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	pollSec, _ := strconv.Atoi(envOr("POLL_INTERVAL_S", "5"))
	snmpPort, _ := strconv.ParseUint(envOr("SNMP_PORT", "161"), 10, 16)
	listenAddr := envOr("LISTEN_ADDR", ":8080")
	bufSize, _ := strconv.Atoi(envOr("RING_BUFFER_SIZE", "240"))

	cfg := snmp.Config{
		Host:         envOr("SNMP_HOST", ""),
		Port:         uint16(snmpPort),
		Username:     envOr("SNMP_USERNAME", ""),
		AuthPass:     envOr("SNMP_AUTH_PASS", ""),
		PrivPass:     envOr("SNMP_PRIV_PASS", ""),
		InterfaceStr: envOr("SNMP_INTERFACE", "sfp12_wan"),
		PollInterval: time.Duration(pollSec) * time.Second,
	}

	if cfg.Host == "" {
		log.Fatal("SNMP_HOST is required")
	}
	if cfg.Username == "" {
		log.Fatal("SNMP_USERNAME is required")
	}

	buf := ringbuf.New(bufSize)
	srv := server.New(buf)

	poller, err := snmp.NewPoller(cfg, buf, srv.Broadcast)
	if err != nil {
		log.Fatalf("failed to create poller: %v", err)
	}

	done := make(chan struct{})
	go poller.Run(done)

	httpSrv := &http.Server{
		Addr:    listenAddr,
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("listening on %s", listenAddr)
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
