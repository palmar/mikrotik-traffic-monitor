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

	"github.com/fsnotify/fsnotify"
	"github.com/palmar/mikrotik-traffic-monitor/internal/config"
	"github.com/palmar/mikrotik-traffic-monitor/internal/report"
	"github.com/palmar/mikrotik-traffic-monitor/internal/ringbuf"
	"github.com/palmar/mikrotik-traffic-monitor/internal/server"
	"github.com/palmar/mikrotik-traffic-monitor/internal/snmp"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	noWatch := flag.Bool("no-watch", false, "disable config file watching")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	srv := server.New()
	shutdown := make(chan struct{})

	// pollerDone is closed to stop the current generation of pollers/scheduler.
	// It is replaced on each config reload.
	pollerDone := make(chan struct{})

	startPollers(cfg, srv, pollerDone)

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

	// reload is a channel that triggers config reload from either SIGHUP or file watcher.
	reload := make(chan struct{}, 1)

	// Set up SIGHUP handler
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-shutdown:
				return
			case <-sighup:
				select {
				case reload <- struct{}{}:
				default:
				}
			}
		}
	}()

	// Set up config file watcher
	if !*noWatch {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			log.Printf("WARNING: config file watching disabled: %v", err)
		} else {
			if err := watcher.Add(*configPath); err != nil {
				log.Printf("WARNING: cannot watch config file %s: %v", *configPath, err)
				watcher.Close()
			} else {
				go watchConfig(watcher, reload, shutdown)
				log.Printf("watching config file %s for changes", *configPath)
			}
		}
	}

	// Main loop: handle reload signals and shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	for {
		select {
		case <-sig:
			log.Println("shutting down")
			close(shutdown)
			close(pollerDone)
			httpSrv.Close()
			return

		case <-reload:
			log.Println("config reload triggered")
			newCfg, err := config.Load(*configPath)
			if err != nil {
				log.Printf("config reload failed (keeping previous config): %v", err)
				continue
			}

			// Stop current pollers and scheduler
			close(pollerDone)

			// Check if listen address changed
			if newCfg.ListenAddr != cfg.ListenAddr {
				log.Printf("NOTE: listen_addr changed from %s to %s (requires restart to take effect)", cfg.ListenAddr, newCfg.ListenAddr)
			}

			// Reset server and start new pollers
			srv.Reset()
			pollerDone = make(chan struct{})
			startPollers(newCfg, srv, pollerDone)

			cfg = newCfg
			log.Println("config reloaded successfully")
		}
	}
}

// startPollers initializes SNMP pollers and the report scheduler from the given config.
func startPollers(cfg *config.Config, srv *server.Server, done chan struct{}) {
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
			log.Printf("WARNING: skipping device %s (unreachable): %v", dev.Name, err)
			continue
		}

		var ifaceNames []string
		buffers := make(map[string]*ringbuf.RingBuffer)
		for _, di := range discovered {
			ifaceNames = append(ifaceNames, di.Name)
			buffers[server.BufKey(dev.Name, di.Name)] = di.Buffer
		}
		sort.Strings(ifaceNames)

		srv.RegisterDevice(dev.Name, ifaceNames, buffers, poller)
		go poller.Run(done)
		log.Printf("device %q: %d interfaces discovered", dev.Name, len(discovered))
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
				log.Printf("WARNING: report scheduler: %v", err)
			} else {
				go scheduler.Run(done)
			}
		} else {
			log.Println("report: no devices have owner_email set, skipping report scheduler")
		}
	}
}

// watchConfig watches the config file for changes and signals the reload channel.
// Debounces rapid writes (e.g. editor save-rename patterns) with a 500ms delay.
func watchConfig(watcher *fsnotify.Watcher, reload chan<- struct{}, shutdown <-chan struct{}) {
	defer watcher.Close()

	var debounce *time.Timer

	for {
		select {
		case <-shutdown:
			if debounce != nil {
				debounce.Stop()
			}
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(500*time.Millisecond, func() {
					select {
					case reload <- struct{}{}:
					default:
					}
				})
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("config watcher error: %v", err)
		}
	}
}
