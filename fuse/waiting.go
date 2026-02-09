package fuse

import (
	"encoding/json"
	"strings"

	"shelley-fuse/shelley"
)

// --- WaitingForInputStatus: analyzes conversation to determine if waiting for user input ---

// WaitingForInputStatus represents the result of analyzing whether a conversation
// is waiting for user input.
type WaitingForInputStatus struct {
	// Waiting is true if the conversation is waiting for user input
	Waiting bool
	// LastAgentIndex is the 0-based index of the last agent message in the messages slice.
	// Only valid if Waiting is true.
	LastAgentIndex int
	// LastAgentSeqID is the sequence ID of the last agent message.
	// Only valid if Waiting is true.
	LastAgentSeqID int
	// LastAgentSlug is the slug of the last agent message (e.g., "agent" or "bash-tool").
	// Only valid if Waiting is true.
	LastAgentSlug string
}

// AnalyzeWaitingForInput determines if a conversation is waiting for user input.
//
// A conversation is waiting for input when:
// - The last content-bearing message (excluding gitinfo) is from agent
// - All tool calls have matching tool results (no pending tool calls)
// - gitinfo messages may follow (ignored for status purposes)
// - No user messages follow the agent
//
// The function returns the status including the index of the last agent message
// for constructing the symlink target.
func AnalyzeWaitingForInput(messages []shelley.Message, toolMap map[string]string) WaitingForInputStatus {
	if len(messages) == 0 {
		return WaitingForInputStatus{Waiting: false}
	}

	// Track pending tool calls (tool calls without matching results)
	pendingToolCalls := make(map[string]bool)

	// Track the last agent message index and slug
	lastAgentIdx := -1
	lastAgentSeqID := 0
	lastAgentSlug := ""

	for i, msg := range messages {
		slug := shelley.MessageSlug(&msg, toolMap)

		// Skip gitinfo messages for status purposes
		if isGitInfoMessage(&msg, slug) {
			continue
		}

		// Check if this is an agent message
		if isAgentMessage(&msg, slug) {
			lastAgentIdx = i
			lastAgentSeqID = msg.SequenceID
			lastAgentSlug = slug

			// Check for tool calls in this agent message
			toolUseIDs := extractToolUseIDs(&msg)
			for _, id := range toolUseIDs {
				pendingToolCalls[id] = true
			}
			continue
		}

		// Check if this is a tool result message
		if isToolResultMessage(&msg, slug) {
			// Mark the corresponding tool call as completed
			toolResultIDs := extractToolResultIDs(&msg)
			for _, id := range toolResultIDs {
				delete(pendingToolCalls, id)
			}
			continue
		}

		// This is a user message (not agent, not tool result, not gitinfo)
		// If there's a user message after the last agent, we're not waiting for input
		// (We'll check this at the end by comparing indices)
	}

	// No agent messages found
	if lastAgentIdx == -1 {
		return WaitingForInputStatus{Waiting: false}
	}

	// Check if there are any pending tool calls
	if len(pendingToolCalls) > 0 {
		return WaitingForInputStatus{Waiting: false}
	}

	// Check if there's a user message after the last agent message
	// (ignoring gitinfo and tool results that complete pending calls)
	for i := lastAgentIdx + 1; i < len(messages); i++ {
		msg := &messages[i]
		slug := shelley.MessageSlug(msg, toolMap)

		// Skip gitinfo messages
		if isGitInfoMessage(msg, slug) {
			continue
		}

		// Tool results are OK (they complete previous tool calls)
		if isToolResultMessage(msg, slug) {
			continue
		}

		// Any other message type after agent means not waiting for input
		// This includes user messages and unexpected message types
		return WaitingForInputStatus{Waiting: false}
	}

	// All conditions met: waiting for input
	return WaitingForInputStatus{
		Waiting:        true,
		LastAgentIndex: lastAgentIdx,
		LastAgentSeqID: lastAgentSeqID,
		LastAgentSlug:  lastAgentSlug,
	}
}

// isAgentMessage returns true if the message is from the agent (LLM).
// Note: Agent messages have Type="shelley". The slug varies based on content:
// - "agent" for text-only responses
// - "{tool}-tool" for messages containing tool calls (e.g., "bash-tool")
func isAgentMessage(msg *shelley.Message, slug string) bool {
	return strings.ToLower(msg.Type) == "shelley"
}

// isGitInfoMessage returns true if the message is a gitinfo message.
// gitinfo messages are ignored for determining conversation status.
func isGitInfoMessage(msg *shelley.Message, slug string) bool {
	// gitinfo messages typically have Type="gitinfo" or similar
	lowerType := strings.ToLower(msg.Type)
	return lowerType == "gitinfo" || lowerType == "git_info" || lowerType == "git-info"
}

// isToolResultMessage returns true if the message contains tool results.
func isToolResultMessage(msg *shelley.Message, slug string) bool {
	// Tool result messages have slugs ending in "-result"
	return strings.HasSuffix(slug, "-result")
}

// extractToolUseIDs extracts all tool use IDs from an agent message.
func extractToolUseIDs(msg *shelley.Message) []string {
	var ids []string

	// Parse content from LLMData
	var data string
	if msg.LLMData != nil {
		data = *msg.LLMData
	}
	if data == "" {
		return ids
	}

	var content shelley.MessageContent
	if err := json.Unmarshal([]byte(data), &content); err != nil {
		return ids
	}

	for _, item := range content.Content {
		if item.Type == shelley.ContentTypeToolUse {
			// Tool use ID can be in either ID or ToolUseID field
			if item.ID != "" {
				ids = append(ids, item.ID)
			}
		}
	}

	return ids
}

// extractToolResultIDs extracts all tool use IDs referenced by tool results in a message.
func extractToolResultIDs(msg *shelley.Message) []string {
	var ids []string

	// Parse content from UserData (tool results are typically in user messages)
	var data string
	if msg.UserData != nil {
		data = *msg.UserData
	}
	if data == "" {
		return ids
	}

	var content shelley.MessageContent
	if err := json.Unmarshal([]byte(data), &content); err != nil {
		return ids
	}

	for _, item := range content.Content {
		if item.Type == shelley.ContentTypeToolResult && item.ToolUseID != "" {
			ids = append(ids, item.ToolUseID)
		}
	}

	return ids
}

// stableIno computes a deterministic inode number from the given key parts.
// This allows go-fuse to reuse existing inodes across repeated Lookup calls
// for the same path, preserving any cached state on the node.
