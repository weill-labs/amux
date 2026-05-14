package server

import (
	"bytes"
	"fmt"

	"github.com/weill-labs/amux/internal/proto"
)

const paneHistoryChunkThreshold = 4 * 1024 * 1024
const paneHistoryBinaryFrameHeaderSize = 9

func chunkPaneHistoryMessages(paneID uint32, history []proto.StyledLine, maxChunkSize int, binaryPaneHistory bool) ([]*Message, error) {
	if len(history) == 0 {
		return nil, nil
	}
	if maxChunkSize <= 0 {
		return nil, fmt.Errorf("invalid pane history chunk size: %d", maxChunkSize)
	}

	messages := make([]*Message, 0, 1)
	for start := 0; start < len(history); {
		end, err := findPaneHistoryChunkEnd(paneID, history, start, maxChunkSize, binaryPaneHistory)
		if err != nil {
			return nil, err
		}
		messages = append(messages, newPaneHistoryMessage(paneID, history[start:end]))
		start = end
	}
	return messages, nil
}

func chunkPaneHistoryMessagesWithCache(paneID uint32, history []proto.StyledLine, maxChunkSize int, binaryPaneHistory bool, cache *proto.PaneHistoryPayloadCache, version uint64) ([]*Message, error) {
	if len(history) == 0 {
		return nil, nil
	}
	if binaryPaneHistory && cache != nil {
		if chunks, ok := cache.ChunkPlan(version, maxChunkSize); ok {
			if messages, ok := paneHistoryMessagesFromCachedChunks(paneID, history, chunks, cache, version); ok {
				return messages, nil
			}
		}
		if payloadLen, ok := cache.PayloadLen(version); ok && payloadLen+paneHistoryBinaryFrameHeaderSize <= maxChunkSize {
			return []*Message{newPaneHistoryMessageWithCache(paneID, history, cache, version)}, nil
		}
	}

	if maxChunkSize <= 0 {
		return nil, fmt.Errorf("invalid pane history chunk size: %d", maxChunkSize)
	}

	messages := make([]*Message, 0, 1)
	chunks := make([]proto.PaneHistoryPayloadChunk, 0, 1)
	for start := 0; start < len(history); {
		end, err := findPaneHistoryChunkEnd(paneID, history, start, maxChunkSize, binaryPaneHistory)
		if err != nil {
			return nil, err
		}
		if binaryPaneHistory && cache != nil {
			messages = append(messages, newPaneHistoryMessageWithCacheRange(paneID, history[start:end], cache, version, start, end))
		} else {
			messages = append(messages, newPaneHistoryMessage(paneID, history[start:end]))
		}
		chunks = append(chunks, proto.PaneHistoryPayloadChunk{Start: start, End: end})
		start = end
	}
	if len(messages) == 1 {
		messages[0].SetPaneHistoryPayloadCache(cache, version)
	} else if binaryPaneHistory && cache != nil {
		cache.StoreChunkPlan(version, maxChunkSize, chunks)
	}
	return messages, nil
}

func findPaneHistoryChunkEnd(paneID uint32, history []proto.StyledLine, start, maxChunkSize int, binaryPaneHistory bool) (int, error) {
	lo, hi := start+1, len(history)
	best := start
	for lo <= hi {
		mid := lo + (hi-lo)/2
		size, err := estimatePaneHistoryMessageSize(newPaneHistoryMessage(paneID, history[start:mid]), binaryPaneHistory)
		if err != nil {
			return 0, err
		}
		if size <= maxChunkSize {
			best = mid
			lo = mid + 1
			continue
		}
		hi = mid - 1
	}
	if best > start {
		return best, nil
	}
	return start + 1, nil
}

func newPaneHistoryMessage(paneID uint32, history []proto.StyledLine) *Message {
	return &Message{
		Type:          MsgTypePaneHistory,
		PaneID:        paneID,
		History:       proto.StyledLineText(history),
		StyledHistory: append([]proto.StyledLine(nil), history...),
	}
}

func newPaneHistoryMessageWithCache(paneID uint32, history []proto.StyledLine, cache *proto.PaneHistoryPayloadCache, version uint64) *Message {
	msg := newPaneHistoryMessage(paneID, history)
	if cache != nil {
		msg.SetPaneHistoryPayloadCache(cache, version)
	}
	return msg
}

func newPaneHistoryMessageWithCacheRange(paneID uint32, history []proto.StyledLine, cache *proto.PaneHistoryPayloadCache, version uint64, start, end int) *Message {
	msg := newPaneHistoryMessage(paneID, history)
	if cache != nil {
		msg.SetPaneHistoryPayloadCacheRange(cache, version, start, end)
	}
	return msg
}

func paneHistoryMessagesFromCachedChunks(paneID uint32, history []proto.StyledLine, chunks []proto.PaneHistoryPayloadChunk, cache *proto.PaneHistoryPayloadCache, version uint64) ([]*Message, bool) {
	messages := make([]*Message, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.Start < 0 || chunk.End <= chunk.Start || chunk.End > len(history) {
			return nil, false
		}
		messages = append(messages, newPaneHistoryMessageWithCacheRange(paneID, history[chunk.Start:chunk.End], cache, version, chunk.Start, chunk.End))
	}
	return messages, true
}

func estimatePaneHistoryMessageSize(msg *Message, binaryPaneHistory bool) (int, error) {
	var buf bytes.Buffer
	writer := proto.NewWriter(&buf)
	writer.SetBinaryPaneHistory(binaryPaneHistory)
	if err := writer.WriteMsg(msg); err != nil {
		return 0, fmt.Errorf("encoding pane history message: %w", err)
	}
	return buf.Len(), nil
}
