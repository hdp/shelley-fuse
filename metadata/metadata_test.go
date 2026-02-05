package metadata

import (
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestMappingApply(t *testing.T) {
	tests := []struct {
		name     string
		mapping  Mapping
		fields   map[string]string
		wantZero bool
		checkFn  func(t *testing.T, ts Timestamps)
	}{
		{
			name:    "empty mapping",
			mapping: Mapping{},
			fields: map[string]string{
				"created_at": "2024-01-15T10:30:00Z",
			},
			wantZero: true,
		},
		{
			name:     "empty fields",
			mapping:  ConversationMapping,
			fields:   map[string]string{},
			wantZero: true,
		},
		{
			name:    "created_at only",
			mapping: ConversationMapping,
			fields: map[string]string{
				"created_at": "2024-01-15T10:30:00Z",
			},
			checkFn: func(t *testing.T, ts Timestamps) {
				expected := mustParseTime("2024-01-15T10:30:00Z")
				if !ts.Ctime.Equal(expected) {
					t.Errorf("Ctime = %v, want %v", ts.Ctime, expected)
				}
				if !ts.Mtime.Equal(expected) {
					t.Errorf("Mtime = %v, want %v", ts.Mtime, expected)
				}
				if !ts.Atime.Equal(expected) {
					t.Errorf("Atime = %v, want %v", ts.Atime, expected)
				}
			},
		},
		{
			name:    "created_at and updated_at",
			mapping: ConversationMapping,
			fields: map[string]string{
				"created_at": "2024-01-15T10:30:00Z",
				"updated_at": "2024-01-16T14:20:00Z",
			},
			checkFn: func(t *testing.T, ts Timestamps) {
				createdAt := mustParseTime("2024-01-15T10:30:00Z")
				updatedAt := mustParseTime("2024-01-16T14:20:00Z")
				// ctime should be created_at
				if !ts.Ctime.Equal(createdAt) {
					t.Errorf("Ctime = %v, want %v", ts.Ctime, createdAt)
				}
				// mtime should be updated_at (overrides created_at)
				if !ts.Mtime.Equal(updatedAt) {
					t.Errorf("Mtime = %v, want %v", ts.Mtime, updatedAt)
				}
				// atime should be created_at (not overridden)
				if !ts.Atime.Equal(createdAt) {
					t.Errorf("Atime = %v, want %v", ts.Atime, createdAt)
				}
			},
		},
		{
			name:    "message mapping",
			mapping: MessageMapping,
			fields: map[string]string{
				"created_at": "2024-01-15T10:30:00Z",
			},
			checkFn: func(t *testing.T, ts Timestamps) {
				expected := mustParseTime("2024-01-15T10:30:00Z")
				if !ts.Ctime.Equal(expected) {
					t.Errorf("Ctime = %v, want %v", ts.Ctime, expected)
				}
				if !ts.Mtime.Equal(expected) {
					t.Errorf("Mtime = %v, want %v", ts.Mtime, expected)
				}
			},
		},
		{
			name:    "invalid timestamp",
			mapping: ConversationMapping,
			fields: map[string]string{
				"created_at": "not-a-timestamp",
			},
			wantZero: true,
		},
		{
			name:    "empty string value",
			mapping: ConversationMapping,
			fields: map[string]string{
				"created_at": "",
			},
			wantZero: true,
		},
		{
			name: "custom mapping",
			mapping: Mapping{
				"my_time": {Mtime: true},
			},
			fields: map[string]string{
				"my_time": "2024-01-15T10:30:00Z",
			},
			checkFn: func(t *testing.T, ts Timestamps) {
				expected := mustParseTime("2024-01-15T10:30:00Z")
				if !ts.Ctime.IsZero() {
					t.Errorf("Ctime should be zero, got %v", ts.Ctime)
				}
				if !ts.Mtime.Equal(expected) {
					t.Errorf("Mtime = %v, want %v", ts.Mtime, expected)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := tt.mapping.Apply(tt.fields)
			if tt.wantZero {
				if !ts.IsZero() {
					t.Errorf("expected zero timestamps, got %+v", ts)
				}
				return
			}
			if tt.checkFn != nil {
				tt.checkFn(t, ts)
			}
		})
	}
}

func TestTimestampsApply(t *testing.T) {
	testTime := mustParseTime("2024-01-15T10:30:00.123456789Z")

	ts := Timestamps{
		Ctime: testTime,
		Mtime: testTime,
		Atime: testTime,
	}

	var attr fuse.Attr
	ts.Apply(&attr)

	if attr.Ctime != uint64(testTime.Unix()) {
		t.Errorf("Ctime = %d, want %d", attr.Ctime, testTime.Unix())
	}
	if attr.Ctimensec != uint32(testTime.Nanosecond()) {
		t.Errorf("Ctimensec = %d, want %d", attr.Ctimensec, testTime.Nanosecond())
	}
	if attr.Mtime != uint64(testTime.Unix()) {
		t.Errorf("Mtime = %d, want %d", attr.Mtime, testTime.Unix())
	}
	if attr.Mtimensec != uint32(testTime.Nanosecond()) {
		t.Errorf("Mtimensec = %d, want %d", attr.Mtimensec, testTime.Nanosecond())
	}
	if attr.Atime != uint64(testTime.Unix()) {
		t.Errorf("Atime = %d, want %d", attr.Atime, testTime.Unix())
	}
	if attr.Atimensec != uint32(testTime.Nanosecond()) {
		t.Errorf("Atimensec = %d, want %d", attr.Atimensec, testTime.Nanosecond())
	}
}

func TestTimestampsApplyPartial(t *testing.T) {
	// Test that Apply only sets non-zero timestamps
	testTime := mustParseTime("2024-01-15T10:30:00Z")

	ts := Timestamps{
		Mtime: testTime,
		// Ctime and Atime are zero
	}

	// Pre-set some values
	var attr fuse.Attr
	attr.Ctime = 12345
	attr.Atime = 67890

	ts.Apply(&attr)

	// Ctime should be unchanged (zero timestamp doesn't overwrite)
	if attr.Ctime != 12345 {
		t.Errorf("Ctime = %d, want 12345 (unchanged)", attr.Ctime)
	}
	// Mtime should be set
	if attr.Mtime != uint64(testTime.Unix()) {
		t.Errorf("Mtime = %d, want %d", attr.Mtime, testTime.Unix())
	}
	// Atime should be unchanged
	if attr.Atime != 67890 {
		t.Errorf("Atime = %d, want 67890 (unchanged)", attr.Atime)
	}
}

func TestTimestampsApplyWithFallback(t *testing.T) {
	testTime := mustParseTime("2024-01-15T10:30:00Z")
	fallbackTime := mustParseTime("2024-01-01T00:00:00Z")

	tests := []struct {
		name      string
		ts        Timestamps
		wantCtime time.Time
		wantMtime time.Time
		wantAtime time.Time
	}{
		{
			name:      "all zero uses fallback",
			ts:        Timestamps{},
			wantCtime: fallbackTime,
			wantMtime: fallbackTime,
			wantAtime: fallbackTime,
		},
		{
			name: "all set ignores fallback",
			ts: Timestamps{
				Ctime: testTime,
				Mtime: testTime,
				Atime: testTime,
			},
			wantCtime: testTime,
			wantMtime: testTime,
			wantAtime: testTime,
		},
		{
			name: "partial uses fallback for zero",
			ts: Timestamps{
				Mtime: testTime,
			},
			wantCtime: fallbackTime,
			wantMtime: testTime,
			wantAtime: fallbackTime,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attr fuse.Attr
			tt.ts.ApplyWithFallback(&attr, fallbackTime)

			if attr.Ctime != uint64(tt.wantCtime.Unix()) {
				t.Errorf("Ctime = %d, want %d", attr.Ctime, tt.wantCtime.Unix())
			}
			if attr.Mtime != uint64(tt.wantMtime.Unix()) {
				t.Errorf("Mtime = %d, want %d", attr.Mtime, tt.wantMtime.Unix())
			}
			if attr.Atime != uint64(tt.wantAtime.Unix()) {
				t.Errorf("Atime = %d, want %d", attr.Atime, tt.wantAtime.Unix())
			}
		})
	}
}

func TestConversationFields(t *testing.T) {
	fields := ConversationFields{
		CreatedAt: "2024-01-15T10:30:00Z",
		UpdatedAt: "2024-01-16T14:20:00Z",
	}

	m := fields.ToMap()

	if m["created_at"] != "2024-01-15T10:30:00Z" {
		t.Errorf("created_at = %q, want %q", m["created_at"], "2024-01-15T10:30:00Z")
	}
	if m["updated_at"] != "2024-01-16T14:20:00Z" {
		t.Errorf("updated_at = %q, want %q", m["updated_at"], "2024-01-16T14:20:00Z")
	}
}

func TestMessageFields(t *testing.T) {
	fields := MessageFields{
		CreatedAt: "2024-01-15T10:30:00Z",
	}

	m := fields.ToMap()

	if m["created_at"] != "2024-01-15T10:30:00Z" {
		t.Errorf("created_at = %q, want %q", m["created_at"], "2024-01-15T10:30:00Z")
	}
}

func TestTimestampsIsZero(t *testing.T) {
	tests := []struct {
		name string
		ts   Timestamps
		want bool
	}{
		{
			name: "all zero",
			ts:   Timestamps{},
			want: true,
		},
		{
			name: "ctime set",
			ts:   Timestamps{Ctime: mustParseTime("2024-01-15T10:30:00Z")},
			want: false,
		},
		{
			name: "mtime set",
			ts:   Timestamps{Mtime: mustParseTime("2024-01-15T10:30:00Z")},
			want: false,
		},
		{
			name: "atime set",
			ts:   Timestamps{Atime: mustParseTime("2024-01-15T10:30:00Z")},
			want: false,
		},
		{
			name: "all set",
			ts: Timestamps{
				Ctime: mustParseTime("2024-01-15T10:30:00Z"),
				Mtime: mustParseTime("2024-01-15T10:30:00Z"),
				Atime: mustParseTime("2024-01-15T10:30:00Z"),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ts.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
