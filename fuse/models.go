package fuse

import (
	"context"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"shelley-fuse/fuse/diag"
	"shelley-fuse/shelley"
	"shelley-fuse/state"
)

// --- ModelsDirNode: /model/ directory listing available models ---

type ModelsDirNode struct {
	fs.Inode
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeLookuper)((*ModelsDirNode)(nil))
var _ = (fs.NodeReaddirer)((*ModelsDirNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelsDirNode)(nil))

func (m *ModelsDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	defer diag.Track(m.diag, "ModelsDirNode", "Lookup", name).Done()

	setEntryTimeout(out, cacheTTLModels)

	// Handle "default" symlink — target uses display name
	if name == "default" {
		defModelID, err := m.client.DefaultModel()
		if err != nil || defModelID == "" {
			return nil, syscall.ENOENT
		}
		// Resolve model ID to display name
		result, err := m.client.ListModels()
		if err != nil {
			return nil, syscall.EIO
		}
		defName := ""
		for _, model := range result.Models {
			if model.ID == defModelID {
				defName = model.Name()
				break
			}
		}
		if defName == "" {
			return nil, syscall.ENOENT
		}
		return m.NewInode(ctx, &SymlinkNode{target: defName, startTime: m.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
	}

	result, err := m.client.ListModels()
	if err != nil {
		return nil, syscall.EIO
	}

	// Primary lookup: match by display name
	for _, model := range result.Models {
		if model.Name() == name {
			return m.NewInode(ctx, &ModelNode{model: model, client: m.client, state: m.state, startTime: m.startTime, diag: m.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
		}
	}
	// Fallback: match by internal ID — return symlink to display name
	for _, model := range result.Models {
		if model.ID != model.Name() && model.ID == name {
			return m.NewInode(ctx, &SymlinkNode{target: model.Name(), startTime: m.startTime}, fs.StableAttr{Mode: syscall.S_IFLNK}), 0
		}
	}
	return nil, syscall.ENOENT
}

func (m *ModelsDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	defer diag.Track(m.diag, "ModelsDirNode", "Readdir", "").Done()
	result, err := m.client.ListModels()
	if err != nil {
		return nil, syscall.EIO
	}

	// Capacity for models + optional default symlink + ID alias symlinks
	entries := make([]fuse.DirEntry, 0, len(result.Models)*2+1)

	// Add "default" symlink if default model is set
	defModelID, defErr := m.client.DefaultModel()
	if defErr == nil && defModelID != "" {
		entries = append(entries, fuse.DirEntry{Name: "default", Mode: syscall.S_IFLNK})
	}

	// Add model directories using display names, plus ID symlinks where they differ
	for _, model := range result.Models {
		entries = append(entries, fuse.DirEntry{Name: model.Name(), Mode: fuse.S_IFDIR})
		if model.ID != model.Name() {
			entries = append(entries, fuse.DirEntry{Name: model.ID, Mode: syscall.S_IFLNK})
		}
	}
	return fs.NewListDirStream(entries), 0
}

func (m *ModelsDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, m.startTime)
	out.SetTimeout(cacheTTLModels)
	return 0
}

// --- ModelNode: /model/{model-id}/ directory for a single model ---

type ModelNode struct {
	fs.Inode
	model     shelley.Model
	client    shelley.ShelleyClient
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeLookuper)((*ModelNode)(nil))
var _ = (fs.NodeReaddirer)((*ModelNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelNode)(nil))

func (m *ModelNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	setEntryTimeout(out, cacheTTLModels)
	switch name {
	case "id":
		return m.NewInode(ctx, &ModelFieldNode{value: m.model.ID, startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "ready":
		// Presence/absence semantics: file exists only when model is ready
		if !m.model.Ready {
			return nil, syscall.ENOENT
		}
		return m.NewInode(ctx, &ModelReadyNode{startTime: m.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "new":
		return m.NewInode(ctx, &ModelNewDirNode{model: m.model, state: m.state, startTime: m.startTime, diag: m.diag}, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
	}
	return nil, syscall.ENOENT
}

func (m *ModelNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{Name: "id", Mode: fuse.S_IFREG},
		{Name: "new", Mode: fuse.S_IFDIR},
	}
	// Presence/absence semantics: only include "ready" if model is ready
	if m.model.Ready {
		entries = append(entries, fuse.DirEntry{Name: "ready", Mode: fuse.S_IFREG})
	}
	return fs.NewListDirStream(entries), 0
}

func (m *ModelNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, m.startTime)
	out.SetTimeout(cacheTTLModels)
	return 0
}

// --- ModelFieldNode: read-only file for a model field (id or ready) ---

type ModelFieldNode struct {
	fs.Inode
	value     string
	startTime time.Time
}

var _ = (fs.NodeOpener)((*ModelFieldNode)(nil))
var _ = (fs.NodeReader)((*ModelFieldNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelFieldNode)(nil))

func (m *ModelFieldNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (m *ModelFieldNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(m.value + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (m *ModelFieldNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = uint64(len(m.value) + 1)
	setTimestamps(&out.Attr, m.startTime)
	out.SetTimeout(cacheTTLModels)
	return 0
}

// --- ModelReadyNode: empty file indicating model is ready (presence/absence semantics) ---

type ModelReadyNode struct {
	fs.Inode
	startTime time.Time
}

var _ = (fs.NodeOpener)((*ModelReadyNode)(nil))
var _ = (fs.NodeReader)((*ModelReadyNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelReadyNode)(nil))

func (m *ModelReadyNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (m *ModelReadyNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// Empty file - presence indicates ready
	return fuse.ReadResultData(nil), 0
}

func (m *ModelReadyNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	out.Size = 0
	setTimestamps(&out.Attr, m.startTime)
	out.SetTimeout(cacheTTLModels)
	return 0
}

// --- ModelNewDirNode: /model/{model-id}/new/ directory containing clone ---

type ModelNewDirNode struct {
	fs.Inode
	model     shelley.Model
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeLookuper)((*ModelNewDirNode)(nil))
var _ = (fs.NodeReaddirer)((*ModelNewDirNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelNewDirNode)(nil))

func (n *ModelNewDirNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	setEntryTimeout(out, cacheTTLModels)
	switch name {
	case "clone":
		return n.NewInode(ctx, &ModelCloneNode{model: n.model, state: n.state, startTime: n.startTime, diag: n.diag}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	case "start":
		return n.NewInode(ctx, &ModelStartNode{model: n.model, startTime: n.startTime}, fs.StableAttr{Mode: fuse.S_IFREG}), 0
	}
	return nil, syscall.ENOENT
}

func (n *ModelNewDirNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream([]fuse.DirEntry{
		{Name: "clone", Mode: fuse.S_IFREG},
		{Name: "start", Mode: fuse.S_IFREG},
	}), 0
}

func (n *ModelNewDirNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFDIR | 0755
	setTimestamps(&out.Attr, n.startTime)
	out.SetTimeout(cacheTTLModels)
	return 0
}

// --- ModelCloneNode: /model/{model-id}/new/clone — clones with model preconfigured ---

type ModelCloneNode struct {
	fs.Inode
	model     shelley.Model
	state     *state.Store
	startTime time.Time
	diag      *diag.Tracker
}

var _ = (fs.NodeOpener)((*ModelCloneNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelCloneNode)(nil))

func (c *ModelCloneNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	defer diag.Track(c.diag, "ModelCloneNode", "Open", c.model.Name()).Done()
	id, err := c.state.Clone()
	if err != nil {
		return nil, 0, syscall.EIO
	}
	// Preconfigure the model on the new conversation
	if err := c.state.SetModel(id, c.model.Name(), c.model.ID); err != nil {
		return nil, 0, syscall.EIO
	}
	return &CloneFileHandle{id: id, diag: c.diag}, fuse.FOPEN_DIRECT_IO, 0
}

func (c *ModelCloneNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0444
	setTimestamps(&out.Attr, c.startTime)
	out.SetTimeout(cacheTTLModels)
	return 0
}

// --- CloneFileHandle: shared file handle for clone nodes ---

type CloneFileHandle struct {
	id   string
	diag *diag.Tracker
}

var _ = (fs.FileReader)((*CloneFileHandle)(nil))

func (h *CloneFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	defer diag.Track(h.diag, "CloneFileHandle", "Read", h.id).Done()
	data := []byte(h.id + "\n")
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

// --- ModelStartNode: /model/{model}/new/start — executable shell script that creates a conversation ---

// modelStartScriptTemplate is the shell script for /model/{model}/new/start.
// It reads a message from stdin, clones a new conversation using the model-specific
// clone file, sets cwd to the caller's working directory, sends the message,
// and prints the conversation ID.
const modelStartScriptTemplate = `#!/bin/sh
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
MOUNT="$(cd "$DIR/../../.." && pwd)"
MSG="$(cat)"
[ -z "$MSG" ] && { echo "error: no message provided on stdin" >&2; exit 1; }
ID="$(cat "$DIR/clone")"
printf 'cwd=%s\n' "$PWD" > "$MOUNT/conversation/$ID/ctl"
printf '%s' "$MSG" > "$MOUNT/conversation/$ID/send"
echo "$ID"
`

type ModelStartNode struct {
	fs.Inode
	model     shelley.Model
	startTime time.Time
}

var _ = (fs.NodeOpener)((*ModelStartNode)(nil))
var _ = (fs.NodeReader)((*ModelStartNode)(nil))
var _ = (fs.NodeGetattrer)((*ModelStartNode)(nil))

func (n *ModelStartNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (n *ModelStartNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data := []byte(modelStartScriptTemplate)
	return fuse.ReadResultData(readAt(data, dest, off)), 0
}

func (n *ModelStartNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = fuse.S_IFREG | 0555
	out.Size = uint64(len(modelStartScriptTemplate))
	setTimestamps(&out.Attr, n.startTime)
	return 0
}
