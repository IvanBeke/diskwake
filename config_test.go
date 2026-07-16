package main

import (
	"os"
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
	cfgYAML := "disks:\n" +
		"  - name: test-disk\n" +
		"    device: /dev/disk/test-disk\n" +
		"    keepalive_interval: 5m\n" +
		"    windows:\n" +
		"      - start: \"08:00\"\n" +
		"        end: \"12:00\"\n" +
		"      - start: \"22:00\"\n" +
		"        end: \"02:00\"\n"
	path := writeTempConfig(t, cfgYAML)

	cfg, err := loadConfig(path)
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

func TestWindowContainsDaySpecificSameDay(t *testing.T) {
	w := Window{Start: "08:00", End: "10:00", hasDayFilter: true, weekdays: []time.Weekday{time.Monday}}
	w.startMinutes = mustParseHHMM(t, w.Start)
	w.endMinutes = mustParseHHMM(t, w.End)

	monday := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)  // Monday
	tuesday := time.Date(2026, 1, 6, 9, 0, 0, 0, time.UTC) // Tuesday

	if !w.contains(monday) {
		t.Fatal("expected monday 09:00 to match day-specific window")
	}
	if w.contains(tuesday) {
		t.Fatal("expected tuesday 09:00 to not match monday-only window")
	}
}

func TestWindowContainsDaySpecificCrossMidnight(t *testing.T) {
	w := Window{Start: "22:00", End: "02:00", hasDayFilter: true, weekdays: []time.Weekday{time.Monday}}
	w.startMinutes = mustParseHHMM(t, w.Start)
	w.endMinutes = mustParseHHMM(t, w.End)

	monLate := time.Date(2026, 1, 5, 23, 0, 0, 0, time.UTC) // Monday
	tueEarly := time.Date(2026, 1, 6, 1, 0, 0, 0, time.UTC) // Tuesday
	tueLate := time.Date(2026, 1, 6, 23, 0, 0, 0, time.UTC) // Tuesday

	if !w.contains(monLate) {
		t.Fatal("expected monday 23:00 to match monday 22:00->02:00 window")
	}
	if !w.contains(tueEarly) {
		t.Fatal("expected tuesday 01:00 to match monday 22:00->02:00 window")
	}
	if w.contains(tueLate) {
		t.Fatal("expected tuesday 23:00 to not match monday 22:00->02:00 window")
	}
}

func TestLoadConfigInvalidDay(t *testing.T) {
	cfgYAML := `
disks:
  - name: bad-day
    device: /dev/disk/bad-day
    keepalive_interval: 5m
    windows:
      - start: "08:00"
        end: "09:00"
        day: "funday"
`
	path := writeTempConfig(t, cfgYAML)

	_, err := loadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid weekday")
	}
}

func TestLoadConfigDayAliases(t *testing.T) {
	cfgYAML := `
disks:
  - name: aliases
    device: /dev/disk/aliases
    keepalive_interval: 5m
    windows:
      - start: "08:00"
        end: "09:00"
        day: "Monday"
      - start: "10:00"
        end: "11:00"
        day: "thu"
`
	path := writeTempConfig(t, cfgYAML)

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("expected valid day aliases, got error: %v", err)
	}

	if !cfg.Disks[0].Windows[0].hasDayFilter || len(cfg.Disks[0].Windows[0].weekdays) != 1 || cfg.Disks[0].Windows[0].weekdays[0] != time.Monday {
		t.Fatal("expected first window to parse as monday")
	}
	if !cfg.Disks[0].Windows[1].hasDayFilter || len(cfg.Disks[0].Windows[1].weekdays) != 1 || cfg.Disks[0].Windows[1].weekdays[0] != time.Thursday {
		t.Fatal("expected second window to parse as thursday")
	}
}

func TestLoadConfigDaysArray(t *testing.T) {
	cfgYAML := `
disks:
  - name: multi-day
    device: /dev/disk/multi-day
    keepalive_interval: 5m
    windows:
      - start: "08:00"
        end: "09:00"
        days: ["mon", "wednesday"]
`
	path := writeTempConfig(t, cfgYAML)

	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("expected valid days array, got error: %v", err)
	}

	w := cfg.Disks[0].Windows[0]
	if !w.contains(time.Date(2026, 1, 5, 8, 30, 0, 0, time.UTC)) { // Monday
		t.Fatal("expected monday to match days filter")
	}
	if !w.contains(time.Date(2026, 1, 7, 8, 30, 0, 0, time.UTC)) { // Wednesday
		t.Fatal("expected wednesday to match days filter")
	}
	if w.contains(time.Date(2026, 1, 6, 8, 30, 0, 0, time.UTC)) { // Tuesday
		t.Fatal("expected tuesday to not match days filter")
	}
}

func TestLoadConfigInvalidDaysEntry(t *testing.T) {
	cfgYAML := `
disks:
  - name: bad-days
    device: /dev/disk/bad-days
    keepalive_interval: 5m
    windows:
      - start: "08:00"
        end: "09:00"
        days: ["monday", "funday"]
`
	path := writeTempConfig(t, cfgYAML)

	_, err := loadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid weekday in days array")
	}
}

func TestLoadConfigRejectsDayAndDaysTogether(t *testing.T) {
	cfgYAML := `
disks:
  - name: both-fields
    device: /dev/disk/both-fields
    keepalive_interval: 5m
    windows:
      - start: "08:00"
        end: "09:00"
        day: "monday"
        days: ["wednesday"]
`
	path := writeTempConfig(t, cfgYAML)

	_, err := loadConfig(path)
	if err == nil {
		t.Fatal("expected error when both day and days are set")
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()

	f, err := os.CreateTemp("", "diskwake-config-*.yaml")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}

	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		t.Fatalf("writing temp config failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing temp config failed: %v", err)
	}

	t.Cleanup(func() {
		_ = os.Remove(f.Name())
	})

	return f.Name()
}

func TestLoadConfigMissingFields(t *testing.T) {
	_, err := loadConfig("does-not-exist.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
