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
			input: "Listen=127.0.0.1:9999 (Stream)",
			want:  "http://127.0.0.1:9999",
		},
		{
			name:  "IPv6 wildcard",
			input: "Listen=[::]:9999 (Stream)",
			want:  "http://localhost:9999",
		},
		{
			name:  "IPv4 wildcard 0.0.0.0",
			input: "Listen=0.0.0.0:8080 (Stream)",
			want:  "http://localhost:8080",
		},
		{
			name:    "unix socket only",
			input:   "Listen=/run/shelley.sock (Stream)",
			wantErr: true,
		},
		{
			name:  "multiple lines with unix socket first",
			input: "Listen=/run/shelley.sock (Stream)\nListen=127.0.0.1:9999 (Stream)",
			want:  "http://127.0.0.1:9999",
		},
		{
			name:  "multiple TCP lines uses first",
			input: "Listen=127.0.0.1:9999 (Stream)\nListen=127.0.0.1:8080 (Stream)",
			want:  "http://127.0.0.1:9999",
		},
		{
			name:    "empty output",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			input:   "  \n  \n  ",
			wantErr: true,
		},
		{
			name:  "without Listen= prefix",
			input: "127.0.0.1:9999 (Stream)",
			want:  "http://127.0.0.1:9999",
		},
		{
			name:  "trailing newline",
			input: "Listen=127.0.0.1:9999 (Stream)\n",
			want:  "http://127.0.0.1:9999",
		},
		{
			name:  "specific IPv4 non-loopback",
			input: "Listen=192.168.1.50:4000 (Stream)",
			want:  "http://192.168.1.50:4000",
		},
		{
			name:  "specific IPv6 loopback",
			input: "Listen=[::1]:9999 (Stream)",
			want:  "http://[::1]:9999",
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
