package fuse

import (
	"sync"

	"shelley-fuse/shelley"
)

// ParsedMessageCache caches parsed messages and toolMaps, keyed by conversation ID.
// The cache is content-addressed: it stores a checksum of the raw data and only
// returns the cached result if the raw data hasn't changed. This ensures that
// all nodes see consistent data — when the upstream CachingClient returns the
// same bytes, parsing is skipped; when it returns new bytes, the cache re-parses.
type ParsedMessageCache struct {
	mu      sync.RWMutex
	entries map[string]*parsedCacheEntry
}

type parsedCacheEntry struct {
	messages []shelley.Message
	toolMap  map[string]string
	maxSeqID int    // highest SequenceID (cached to avoid O(N) recomputation)
	checksum uint64 // FNV-1a hash of the raw data used to produce this entry
	rawData  []byte // reference to the raw data slice for fast identity checks
}

// NewParsedMessageCache creates a new content-addressed parse cache.
func NewParsedMessageCache() *ParsedMessageCache {
	return &ParsedMessageCache{
		entries: make(map[string]*parsedCacheEntry),
	}
}

// dataChecksum computes a fast FNV-1a hash of the raw data.
func dataChecksum(data []byte) uint64 {
	// FNV-1a 64-bit
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	h := uint64(offset64)
	for _, b := range data {
		h ^= uint64(b)
		h *= prime64
	}
	return h
}

// ParseResult holds the result of parsing conversation data.
type ParseResult struct {
	Messages []shelley.Message
	ToolMap  map[string]string
	MaxSeqID int
}

// GetOrParse returns cached messages and toolMap for a conversation, or parses the data and caches it.
// The rawData is the JSON response from GetConversation. The cache returns the previously parsed
// result only if rawData has the same content; otherwise it re-parses and caches.
// It first checks if rawData is the exact same slice (pointer identity) for O(1) cache hits
// when the CachingClient returns the same cached bytes, then falls back to FNV checksum comparison.
func (c *ParsedMessageCache) GetOrParse(conversationID string, rawData []byte) ([]shelley.Message, map[string]string, error) {
	r, err := c.GetOrParseResult(conversationID, rawData)
	if err != nil {
		return nil, nil, err
	}
	return r.Messages, r.ToolMap, nil
}

// GetOrParseResult is like GetOrParse but returns the full ParseResult including MaxSeqID.
func (c *ParsedMessageCache) GetOrParseResult(conversationID string, rawData []byte) (*ParseResult, error) {
	if c != nil {
		c.mu.RLock()
		entry := c.entries[conversationID]
		c.mu.RUnlock()

		if entry != nil {
			// Fast path: pointer identity check. When CachingClient returns
			// the same cached slice, this avoids computing the checksum entirely.
			if len(rawData) == len(entry.rawData) && len(rawData) > 0 &&
				&rawData[0] == &entry.rawData[0] {
				return &ParseResult{Messages: entry.messages, ToolMap: entry.toolMap, MaxSeqID: entry.maxSeqID}, nil
			}
			// Slow path: content-addressed comparison via checksum
			if entry.checksum == dataChecksum(rawData) {
				return &ParseResult{Messages: entry.messages, ToolMap: entry.toolMap, MaxSeqID: entry.maxSeqID}, nil
			}
		}
	}

	// Parse the conversation data
	msgs, err := shelley.ParseMessages(rawData)
	if err != nil {
		return nil, err
	}

	// Build the tool name map
	msgPtrs := make([]*shelley.Message, len(msgs))
	for i := range msgs {
		msgPtrs[i] = &msgs[i]
	}
	toolMap := shelley.BuildToolNameMap(msgPtrs)
	maxSeq := maxSeqIDFromMessages(msgs)

	// Cache the result
	if c != nil {
		c.mu.Lock()
		c.entries[conversationID] = &parsedCacheEntry{
			messages: msgs,
			toolMap:  toolMap,
			maxSeqID: maxSeq,
			checksum: dataChecksum(rawData),
			rawData:  rawData,
		}
		c.mu.Unlock()
	}

	return &ParseResult{Messages: msgs, ToolMap: toolMap, MaxSeqID: maxSeq}, nil
}

// Invalidate removes the cached entry for a conversation.
// Safe to call on nil receiver.
func (c *ParsedMessageCache) Invalidate(conversationID string) {
	if c != nil {
		c.mu.Lock()
		delete(c.entries, conversationID)
		c.mu.Unlock()
	}
}
