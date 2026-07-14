package main

import "testing"

func TestParsePortSettingValid(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "plain", input: "8080", want: 8080},
		{name: "prefixed", input: ":9090", want: 9090},
		{name: "trimmed", input: "  1234  ", want: 1234},
		{name: "max", input: "65535", want: 65535},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePortSetting(tc.input)
			if err != nil {
				t.Fatalf("parsePortSetting(%q) returned error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("parsePortSetting(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestParsePortSettingInvalid(t *testing.T) {
	tests := []string{
		"",
		"0",
		"65536",
		"abc",
		"0.0.0.0:8080",
		"127.0.0.1",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := parsePortSetting(input); err == nil {
				t.Fatalf("parsePortSetting(%q) expected error, got nil", input)
			}
		})
	}
}

func TestResolveHTTPListenPortSourceOrder(t *testing.T) {
	t.Setenv("DISKWAKE_PORT", "9090")

	gotPort, gotSource, err := resolveHTTPListenPort("7777")
	if err != nil {
		t.Fatalf("resolveHTTPListenPort(flag) returned error: %v", err)
	}
	if gotPort != 7777 || gotSource != "flag:--port" {
		t.Fatalf("resolveHTTPListenPort(flag) = (%d, %q), want (7777, %q)", gotPort, gotSource, "flag:--port")
	}

	gotPort, gotSource, err = resolveHTTPListenPort("")
	if err != nil {
		t.Fatalf("resolveHTTPListenPort(env) returned error: %v", err)
	}
	if gotPort != 9090 || gotSource != "env:DISKWAKE_PORT" {
		t.Fatalf("resolveHTTPListenPort(env) = (%d, %q), want (9090, %q)", gotPort, gotSource, "env:DISKWAKE_PORT")
	}

	t.Setenv("DISKWAKE_PORT", "")
	gotPort, gotSource, err = resolveHTTPListenPort("")
	if err != nil {
		t.Fatalf("resolveHTTPListenPort(default) returned error: %v", err)
	}
	if gotPort != 8080 || gotSource != "default" {
		t.Fatalf("resolveHTTPListenPort(default) = (%d, %q), want (8080, %q)", gotPort, gotSource, "default")
	}
}
