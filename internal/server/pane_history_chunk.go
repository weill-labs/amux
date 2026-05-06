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
		if payloadLen, ok := cache.PayloadLen(version); ok && payloadLen+paneHistoryBinaryFrameHeaderSize <= maxChunkSize {
			return []*Message{newPaneHistoryMessageWithCache(paneID, history, cache, version)}, nil
		}
	}

	messages, err := chunkPaneHistoryMessages(paneID, history, maxChunkSize, binaryPaneHistory)
	if err != nil {
		return nil, err
	}
	if len(messages) == 1 {
		messages[0].SetPaneHistoryPayloadCache(cache, version)
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

func estimatePaneHistoryMessageSize(msg *Message, binaryPaneHistory bool) (int, error) {
	var buf bytes.Buffer
	writer := proto.NewWriter(&buf)
	writer.SetBinaryPaneHistory(binaryPaneHistory)
	if err := writer.WriteMsg(msg); err != nil {
		return 0, fmt.Errorf("encoding pane history message: %w", err)
	}
	return buf.Len(), nil
}
