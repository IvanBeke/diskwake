package main

import (
    "flag"
    "log"
    "os"
    "os/signal"
    "sync"
    "syscall"
    "time"
)

func main() {
    configPath := flag.String("config", "/etc/diskwake/config.yaml", "path to YAML config file")
    flag.Parse()

    logger := log.New(os.Stdout, "", log.LstdFlags)

    cfg, err := loadConfig(*configPath)
    if err != nil {
        logger.Fatalf("config error: %v", err)
    }

    logger.Printf("loaded config: %d disk(s) configured", len(cfg.Disks))
    for _, d := range cfg.Disks {
        logger.Printf("  - %s (%s): interval=%s windows=%v", d.Name, d.Device, d.interval, d.Windows)
    }

    ctx, cancel := signalContext()
    defer cancel()

    var wg sync.WaitGroup
    for _, d := range cfg.Disks {
        wg.Add(1)
        go func(disk DiskConfig) {
            defer wg.Done()
            runDiskLoop(ctx, logger, disk)
        }(d)
    }

    wg.Wait()
    logger.Println("all disk loops stopped, exiting")
}

// runDiskLoop periodically checks whether the current time falls within one
// of the disk's configured windows, and if so performs a keep-awake read.
// Outside all windows, it does nothing, allowing the drive/enclosure's own
// default idle timer to spin it down naturally.
func runDiskLoop(stop <-chan struct{}, logger *log.Logger, disk DiskConfig) {
    logger.Printf("[%s] starting loop (interval=%s)", disk.Name, disk.interval)

    ticker := time.NewTicker(disk.interval)
    defer ticker.Stop()

    // Check immediately on startup rather than waiting a full interval.
    tick(logger, disk)

    for {
        select {
        case <-stop:
            logger.Printf("[%s] stopping loop", disk.Name)
            return
        case <-ticker.C:
            tick(logger, disk)
        }
    }
}

func tick(logger *log.Logger, disk DiskConfig) {
    now := time.Now()

    if !disk.inAnyWindow(now) {
        logger.Printf("[%s] outside configured windows, leaving idle", disk.Name)
        return
    }

    if err := keepAwake(disk.Device); err != nil {
        logger.Printf("[%s] keep-awake read failed: %v", disk.Name, err)
        return
    }

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
