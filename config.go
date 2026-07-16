package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// Window represents a single "keep awake" time range in HH:MM 24h format.
// End may be earlier than Start to represent a range that crosses midnight.
type Window struct {
	Start string `yaml:"start"`
	End   string `yaml:"end"`
	Day   string `yaml:"day,omitempty"`
	Days  []string `yaml:"days,omitempty"`

	startMinutes int
	endMinutes   int
	hasDayFilter bool
	weekdays     []time.Weekday
}

// DiskConfig describes a single disk to manage.
type DiskConfig struct {
	Name              string   `yaml:"name"`
	Device            string   `yaml:"device"`
	KeepaliveInterval string   `yaml:"keepalive_interval"`
	Windows           []Window `yaml:"windows"`

	interval time.Duration
}

// Config is the top level YAML structure.
type Config struct {
	Disks []DiskConfig `yaml:"disks"`
}

// loadConfig reads and validates the YAML config file at path.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	if len(cfg.Disks) == 0 {
		return nil, fmt.Errorf("config has no disks defined")
	}

	for i := range cfg.Disks {
		d := &cfg.Disks[i]

		if d.Name == "" {
			return nil, fmt.Errorf("disk at index %d is missing a name", i)
		}
		if d.Device == "" {
			return nil, fmt.Errorf("disk %q is missing a device path", d.Name)
		}
		if len(d.Windows) == 0 {
			return nil, fmt.Errorf("disk %q has no windows defined", d.Name)
		}

		interval, err := time.ParseDuration(d.KeepaliveInterval)
		if err != nil {
			return nil, fmt.Errorf("disk %q has invalid keepalive_interval %q: %w", d.Name, d.KeepaliveInterval, err)
		}
		if interval <= 0 {
			return nil, fmt.Errorf("disk %q keepalive_interval must be positive", d.Name)
		}
		d.interval = interval

		for wi := range d.Windows {
			w := &d.Windows[wi]
			if strings.TrimSpace(w.Day) != "" && len(w.Days) > 0 {
				return nil, fmt.Errorf("disk %q window %d cannot set both day and days", d.Name, wi)
			}
			startMin, err := parseHHMM(w.Start)
			if err != nil {
				return nil, fmt.Errorf("disk %q window %d has invalid start %q: %w", d.Name, wi, w.Start, err)
			}
			endMin, err := parseHHMM(w.End)
			if err != nil {
				return nil, fmt.Errorf("disk %q window %d has invalid end %q: %w", d.Name, wi, w.End, err)
			}
			if startMin == endMin {
				return nil, fmt.Errorf("disk %q window %d: start and end cannot be identical", d.Name, wi)
			}

			dayValues := make([]string, 0, len(w.Days)+1)
			if w.Day != "" {
				dayValues = append(dayValues, w.Day)
			}
			dayValues = append(dayValues, w.Days...)

			if len(dayValues) > 0 {
				w.hasDayFilter = true
				w.weekdays = make([]time.Weekday, 0, len(dayValues))
				seen := make(map[time.Weekday]struct{}, len(dayValues))
				for _, day := range dayValues {
					weekday, err := parseWeekday(day)
					if err != nil {
						return nil, fmt.Errorf("disk %q window %d has invalid day %q: %w", d.Name, wi, day, err)
					}
					if _, ok := seen[weekday]; ok {
						continue
					}
					seen[weekday] = struct{}{}
					w.weekdays = append(w.weekdays, weekday)
				}
			}

			w.startMinutes = startMin
			w.endMinutes = endMin
		}
	}

	return &cfg, nil
}

// parseHHMM parses a "HH:MM" 24-hour string into minutes since midnight.
func parseHHMM(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

func parseWeekday(s string) (time.Weekday, error) {
	switch normalizeWeekday(s) {
	case "sun", "sunday":
		return time.Sunday, nil
	case "mon", "monday":
		return time.Monday, nil
	case "tue", "tues", "tuesday":
		return time.Tuesday, nil
	case "wed", "wednesday":
		return time.Wednesday, nil
	case "thu", "thur", "thurs", "thursday":
		return time.Thursday, nil
	case "fri", "friday":
		return time.Friday, nil
	case "sat", "saturday":
		return time.Saturday, nil
	default:
		return 0, fmt.Errorf("expected weekday name like monday/mon")
	}
}

func normalizeWeekday(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func containsWeekday(days []time.Weekday, day time.Weekday) bool {
	for _, d := range days {
		if d == day {
			return true
		}
	}
	return false
}

func (w Window) matchesConfiguredDay(t time.Time, minutes int, crossesMidnight bool) bool {
	if !w.hasDayFilter {
		return true
	}

	wd := t.Weekday()
	if !crossesMidnight {
		return containsWeekday(w.weekdays, wd)
	}

	for _, day := range w.weekdays {
		next := (day + 1) % 7
		if wd == day && minutes >= w.startMinutes {
			return true
		}
		if wd == next && minutes < w.endMinutes {
			return true
		}
	}

	return false
}

// contains reports whether the given time falls inside this window.
// Handles windows that cross midnight (e.g. start=22:00, end=02:00).
func (w Window) contains(t time.Time) bool {
	minutes := t.Hour()*60 + t.Minute()
	crossesMidnight := w.startMinutes > w.endMinutes

	if !w.matchesConfiguredDay(t, minutes, crossesMidnight) {
		return false
	}

	if w.startMinutes <= w.endMinutes {
		return minutes >= w.startMinutes && minutes < w.endMinutes
	}
	// Crosses midnight.
	return minutes >= w.startMinutes || minutes < w.endMinutes
}

// inAnyWindow reports whether t falls inside any of the disk's configured windows.
func (d DiskConfig) inAnyWindow(t time.Time) bool {
	for _, w := range d.Windows {
		if w.contains(t) {
			return true
		}
	}
	return false
}
