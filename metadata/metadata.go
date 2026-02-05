// Package metadata provides a mapping system for JSON timestamp fields to filesystem stat attributes.
//
// This package enables uniform mapping of JSON metadata (like created_at, updated_at) to
// filesystem attributes (ctime, mtime) when representing objects as directories.
//
// Example usage:
//
//	// Define a mapping for conversation directories
//	mapping := metadata.Mapping{
//		"created_at": {Ctime: true, Mtime: true},
//		"updated_at": {Mtime: true},
//	}
//
//	// Apply the mapping to extract timestamps
//	timestamps := mapping.Apply(map[string]string{
//		"created_at": "2024-01-15T10:30:00Z",
//		"updated_at": "2024-01-16T14:20:00Z",
//	})
//
//	// Use timestamps in Getattr
//	timestamps.Apply(&out.Attr)
package metadata

import (
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
)

// AttributeTarget specifies which filesystem attributes a field should populate.
type AttributeTarget struct {
	// Ctime indicates the field should set the change time (ctime).
	Ctime bool
	// Mtime indicates the field should set the modification time (mtime).
	Mtime bool
	// Atime indicates the field should set the access time (atime).
	Atime bool
}

// Mapping defines how JSON field names map to filesystem attributes.
// Each key is a field name (e.g., "created_at"), and the value specifies
// which attributes that field should populate.
type Mapping map[string]AttributeTarget

// Timestamps holds the resolved filesystem timestamps after applying a mapping.
type Timestamps struct {
	Ctime time.Time
	Mtime time.Time
	Atime time.Time
}

// IsZero returns true if all timestamps are zero.
func (t Timestamps) IsZero() bool {
	return t.Ctime.IsZero() && t.Mtime.IsZero() && t.Atime.IsZero()
}

// Apply sets the timestamps on a fuse.Attr struct.
// Only non-zero timestamps are applied.
func (t Timestamps) Apply(attr *fuse.Attr) {
	if !t.Ctime.IsZero() {
		attr.Ctime = uint64(t.Ctime.Unix())
		attr.Ctimensec = uint32(t.Ctime.Nanosecond())
	}
	if !t.Mtime.IsZero() {
		attr.Mtime = uint64(t.Mtime.Unix())
		attr.Mtimensec = uint32(t.Mtime.Nanosecond())
	}
	if !t.Atime.IsZero() {
		attr.Atime = uint64(t.Atime.Unix())
		attr.Atimensec = uint32(t.Atime.Nanosecond())
	}
}

// ApplyWithFallback sets the timestamps on a fuse.Attr struct, using a fallback
// time for any timestamp that is zero.
func (t Timestamps) ApplyWithFallback(attr *fuse.Attr, fallback time.Time) {
	ctime := t.Ctime
	if ctime.IsZero() {
		ctime = fallback
	}
	attr.Ctime = uint64(ctime.Unix())
	attr.Ctimensec = uint32(ctime.Nanosecond())

	mtime := t.Mtime
	if mtime.IsZero() {
		mtime = fallback
	}
	attr.Mtime = uint64(mtime.Unix())
	attr.Mtimensec = uint32(mtime.Nanosecond())

	atime := t.Atime
	if atime.IsZero() {
		atime = fallback
	}
	attr.Atime = uint64(atime.Unix())
	attr.Atimensec = uint32(atime.Nanosecond())
}

// Apply extracts timestamps from the given fields according to the mapping.
// Fields should be a map of field name to string value (RFC3339 timestamp).
// Fields are processed in deterministic order, with later mappings overwriting earlier ones.
func (m Mapping) Apply(fields map[string]string) Timestamps {
	var ts Timestamps

	// Process fields in a defined order for deterministic behavior.
	// We process created_at first, then updated_at, so updated_at can override mtime.
	orderedFields := []string{"created_at", "updated_at", "modified_at"}

	// First pass: process known fields in order
	for _, fieldName := range orderedFields {
		target, hasMapping := m[fieldName]
		if !hasMapping {
			continue
		}
		value, hasValue := fields[fieldName]
		if !hasValue || value == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, value)
		if err != nil {
			continue
		}
		applyTarget(&ts, t, target)
	}

	// Second pass: process any other fields not in the ordered list
	for fieldName, target := range m {
		// Skip if already processed
		skip := false
		for _, known := range orderedFields {
			if fieldName == known {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		value, hasValue := fields[fieldName]
		if !hasValue || value == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, value)
		if err != nil {
			continue
		}
		applyTarget(&ts, t, target)
	}

	return ts
}

func applyTarget(ts *Timestamps, t time.Time, target AttributeTarget) {
	if target.Ctime {
		ts.Ctime = t
	}
	if target.Mtime {
		ts.Mtime = t
	}
	if target.Atime {
		ts.Atime = t
	}
}

// Predefined mappings for common node types.
var (
	// ConversationMapping maps conversation metadata to filesystem attributes.
	// - created_at sets both ctime and mtime (as initial values)
	// - updated_at overrides mtime (if available)
	ConversationMapping = Mapping{
		"created_at": {Ctime: true, Mtime: true, Atime: true},
		"updated_at": {Mtime: true},
	}

	// MessageMapping maps message metadata to filesystem attributes.
	// - created_at sets both ctime and mtime
	MessageMapping = Mapping{
		"created_at": {Ctime: true, Mtime: true, Atime: true},
	}
)

// ConversationFields extracts field values from a conversation-like struct.
// Accepts any struct with CreatedAt and UpdatedAt string fields.
type ConversationFields struct {
	CreatedAt string
	UpdatedAt string
}

// ToMap converts ConversationFields to a map for use with Mapping.Apply.
func (c ConversationFields) ToMap() map[string]string {
	return map[string]string{
		"created_at": c.CreatedAt,
		"updated_at": c.UpdatedAt,
	}
}

// MessageFields extracts field values from a message-like struct.
type MessageFields struct {
	CreatedAt string
}

// ToMap converts MessageFields to a map for use with Mapping.Apply.
func (m MessageFields) ToMap() map[string]string {
	return map[string]string{
		"created_at": m.CreatedAt,
	}
}
