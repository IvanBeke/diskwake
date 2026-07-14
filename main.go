package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "/etc/diskwake/config.yaml", "path to YAML config file")
	httpPort := flag.String("port", "", "read-only web UI port (for example 8080); default is 8080")
	flag.Parse()

	bootstrapLogger := log.New(os.Stdout, "", log.LstdFlags)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		bootstrapLogger.Fatalf("config error: %v", err)
	}

	rawConfig, err := os.ReadFile(*configPath)
	if err != nil {
		bootstrapLogger.Fatalf("config read error: %v", err)
	}

	state := newRuntimeState(cfg, *configPath, string(rawConfig), 1000)
	logger := log.New(io.MultiWriter(os.Stdout, newStateLogWriter(state)), "", log.LstdFlags)

	logger.Printf("loaded config: %d disk(s) configured", len(cfg.Disks))
	for _, d := range cfg.Disks {
		logger.Printf("  - %s (%s): interval=%s windows=%v", d.Name, d.Device, d.interval, d.Windows)
	}

	ctx, cancel := signalContext()
	defer cancel()

	resolvedHTTPPort, httpPortSource, err := resolveHTTPListenPort(*httpPort)
	if err != nil {
		logger.Fatalf("invalid web ui port: %v", err)
	}
	resolvedHTTPAddr := ":" + strconv.Itoa(resolvedHTTPPort)
	logger.Printf("web ui listen port resolved to %d (source=%s)", resolvedHTTPPort, httpPortSource)
	if err := startWebUI(ctx, logger, resolvedHTTPAddr, state); err != nil {
		logger.Fatalf("web ui error: %v", err)
	}

	var wg sync.WaitGroup
	for _, d := range cfg.Disks {
		wg.Add(1)
		go func(disk DiskConfig) {
			defer wg.Done()
			runDiskLoop(ctx, logger, disk, state)
		}(d)
	}

	wg.Wait()
	logger.Println("all disk loops stopped, exiting")
}

// runDiskLoop periodically checks whether the current time falls within one
// of the disk's configured windows, and if so performs a keep-awake read.
// Outside all windows, it does nothing, allowing the drive/enclosure's own
// default idle timer to spin it down naturally.
func runDiskLoop(stop <-chan struct{}, logger *log.Logger, disk DiskConfig, state *runtimeState) {
	logger.Printf("[%s] starting loop (interval=%s)", disk.Name, disk.interval)

	ticker := time.NewTicker(disk.interval)
	defer ticker.Stop()

	// Check immediately on startup rather than waiting a full interval.
	tick(logger, disk, state)

	for {
		select {
		case <-stop:
			logger.Printf("[%s] stopping loop", disk.Name)
			return
		case <-ticker.C:
			tick(logger, disk, state)
		}
	}
}

func tick(logger *log.Logger, disk DiskConfig, state *runtimeState) {
	now := time.Now()

	if !disk.inAnyWindow(now) {
		state.recordOutsideWindow(disk.Name, now)
		logger.Printf("[%s] outside configured windows, leaving idle", disk.Name)
		return
	}

	if err := keepAwake(disk.Device); err != nil {
		state.recordReadResult(disk.Name, now, err)
		logger.Printf("[%s] keep-awake read failed: %v", disk.Name, err)
		return
	}

	state.recordReadResult(disk.Name, now, nil)

	logger.Printf("[%s] keep-awake read OK", disk.Name)
}

// signalContext returns a channel that closes when SIGINT or SIGTERM is
// received, for graceful shutdown under `docker stop`.
func signalContext() (<-chan struct{}, func()) {
	stop := make(chan struct{})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigs
		close(stop)
	}()

	return stop, func() { signal.Stop(sigs) }
}

func resolveHTTPListenPort(flagValue string) (int, string, error) {
	if v := strings.TrimSpace(flagValue); v != "" {
		port, err := parsePortSetting(v)
		if err != nil {
			return 0, "", err
		}
		return port, "flag:--port", nil
	}

	if v := strings.TrimSpace(os.Getenv("DISKWAKE_PORT")); v != "" {
		port, err := parsePortSetting(v)
		if err != nil {
			return 0, "", err
		}
		return port, "env:DISKWAKE_PORT", nil
	}

	return 8080, "default", nil
}

func parsePortSetting(v string) (int, error) {
	value := strings.TrimSpace(v)
	if strings.HasPrefix(value, ":") {
		value = strings.TrimPrefix(value, ":")
	}
	if strings.Contains(value, ":") {
		return 0, fmt.Errorf("must be a port number only (no host/address): %q", v)
	}

	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("must be a valid integer port: %q", v)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("must be between 1 and 65535: %d", port)
	}

	return port, nil
}
