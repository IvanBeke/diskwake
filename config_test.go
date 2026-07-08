package main

import (
    "testing"
    "time"
)

func mustParseHHMM(t *testing.T, s string) int {
    t.Helper()
    m, err := parseHHMM(s)
    if err != nil {
        t.Fatalf("parseHHMM(%q) failed: %v", s, err)
    }
    return m
}

func TestWindowContainsNormalRange(t *testing.T) {
    w := Window{Start: "08:00", End: "13:00"}
    w.startMinutes = mustParseHHMM(t, w.Start)
    w.endMinutes = mustParseHHMM(t, w.End)

    cases := []struct {
        hh, mm int
        want   bool
    }{
        {7, 59, false},
        {8, 0, true},
        {10, 30, true},
        {12, 59, true},
        {13, 0, false},
        {13, 1, false},
    }

    for _, c := range cases {
        got := w.contains(time.Date(2026, 1, 1, c.hh, c.mm, 0, 0, time.UTC))
        if got != c.want {
            t.Errorf("contains(%02d:%02d) = %v, want %v", c.hh, c.mm, got, c.want)
        }
    }
}

func TestWindowContainsCrossesMidnight(t *testing.T) {
    w := Window{Start: "22:00", End: "02:00"}
    w.startMinutes = mustParseHHMM(t, w.Start)
    w.endMinutes = mustParseHHMM(t, w.End)

    cases := []struct {
        hh, mm int
        want   bool
    }{
        {21, 59, false},
        {22, 0, true},
        {23, 30, true},
        {0, 0, true},
        {1, 59, true},
        {2, 0, false},
        {12, 0, false},
    }

    for _, c := range cases {
        got := w.contains(time.Date(2026, 1, 1, c.hh, c.mm, 0, 0, time.UTC))
        if got != c.want {
            t.Errorf("contains(%02d:%02d) = %v, want %v", c.hh, c.mm, got, c.want)
        }
    }
}

func TestLoadConfigValid(t *testing.T) {
    cfg, err := loadConfig("test-config.yaml")
    if err != nil {
        t.Fatalf("loadConfig failed: %v", err)
    }
    if len(cfg.Disks) != 1 {
        t.Fatalf("expected 1 disk, got %d", len(cfg.Disks))
    }
    d := cfg.Disks[0]
    if d.interval != 5*time.Minute {
        t.Errorf("expected 5m interval, got %v", d.interval)
    }
    if len(d.Windows) != 2 {
        t.Errorf("expected 2 windows, got %d", len(d.Windows))
    }

    // 09:00 should be in the first window.
    if !d.inAnyWindow(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)) {
        t.Errorf("expected 09:00 to be in a window")
    }
    // 15:00 should be in no window.
    if d.inAnyWindow(time.Date(2026, 1, 1, 15, 0, 0, 0, time.UTC)) {
        t.Errorf("expected 15:00 to be outside all windows")
    }
    // 23:30 should be in the midnight-crossing window.
    if !d.inAnyWindow(time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC)) {
        t.Errorf("expected 23:30 to be in a window")
    }
}

func TestLoadConfigMissingFields(t *testing.T) {
    _, err := loadConfig("does-not-exist.yaml")
    if err == nil {
        t.Fatal("expected error for missing file, got nil")
    }
}
