package main

import (
	"testing"
)

func TestParseListenAddress(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "specific IPv4 address",
			input: `[{"listen":"127.0.0.1:9999","unit":"shelley.socket","activates":"shelley.service"}]`,
			want:  "http://127.0.0.1:9999",
		},
		{
			name:  "IPv6 wildcard converts to localhost",
			input: `[{"listen":"[::]:9999","unit":"shelley.socket","activates":"shelley.service"}]`,
			want:  "http://localhost:9999",
		},
		{
			name:  "IPv4 wildcard 0.0.0.0 converts to localhost",
			input: `[{"listen":"0.0.0.0:8080","unit":"shelley.socket","activates":"shelley.service"}]`,
			want:  "http://localhost:8080",
		},
		{
			name:  "empty wildcard becomes localhost",
			input: `[{"listen":":8080","unit":"shelley.socket","activates":"shelley.service"}]`,
			want:  "http://localhost:8080",
		},
		{
			name:    "unix socket only should error",
			input:   `[{"listen":"/run/shelley.sock","unit":"shelley.socket","activates":"shelley.service"}]`,
			wantErr: true,
		},
		{
			name:  "multiple entries with unix socket first finds TCP",
			input: `[{"listen":"/run/shelley.sock","unit":"shelley.socket","activates":"shelley.service"},{"listen":"127.0.0.1:9999","unit":"shelley.socket","activates":"shelley.service"}]`,
			want:  "http://127.0.0.1:9999",
		},
		{
			name:  "multiple TCP entries uses first",
			input: `[{"listen":"127.0.0.1:9999","unit":"shelley.socket","activates":"shelley.service"},{"listen":"127.0.0.1:8080","unit":"shelley.socket","activates":"shelley.service"}]`,
			want:  "http://127.0.0.1:9999",
		},
		{
			name:    "empty JSON array should error",
			input:   `[]`,
			wantErr: true,
		},
		{
			name:    "invalid JSON should error",
			input:   `not valid json`,
			wantErr: true,
		},
		{
			name:  "specific IPv4 non-loopback",
			input: `[{"listen":"192.168.1.50:4000","unit":"shelley.socket","activates":"shelley.service"}]`,
			want:  "http://192.168.1.50:4000",
		},
		{
			name:  "specific IPv6 loopback",
			input: `[{"listen":"[::1]:9999","unit":"shelley.socket","activates":"shelley.service"}]`,
			want:  "http://[::1]:9999",
		},
		{
			name:    "JSON with other units only should error",
			input:   `[{"listen":"/var/run/dbus/socket","unit":"dbus.socket","activates":"dbus.service"}]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseListenAddress(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseListenAddress(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseListenAddress(%q) returned error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("parseListenAddress(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDiscoverBackendURL(t *testing.T) {
	// This is an integration test that requires shelley.socket to exist.
	// It will fail if the socket doesn't exist or isn't listening on a TCP port.
	// For now we skip it in automated CI environments.
	t.Skip("integration test requiring real shelley.socket")
	
	url := discoverBackendURL()
	if url == "" {
		t.Error("discoverBackendURL returned empty string")
	}
	if url == defaultBackendURL {
		t.Log("using default backend URL")
	}
	t.Logf("discovered URL: %s", url)
}
