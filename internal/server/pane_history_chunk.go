package server

import (
	"bytes"
	"fmt"

	"github.com/weill-labs/amux/internal/proto"
)

const paneHistoryChunkThreshold = 4 * 1024 * 1024

func chunkPaneHistoryMessages(paneID uint32, history []proto.StyledLine, maxChunkSize int) ([]*Message, error) {
	return chunkPaneHistoryMessagesWithEncoding(paneID, history, maxChunkSize, false)
}

func chunkPaneHistoryMessagesWithEncoding(paneID uint32, history []proto.StyledLine, maxChunkSize int, binaryPaneHistory bool) ([]*Message, error) {
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

func findPaneHistoryChunkEnd(paneID uint32, history []proto.StyledLine, start, maxChunkSize int, binaryPaneHistory bool) (int, error) {
	lo, hi := start+1, len(history)
	best := start
	for lo <= hi {
		mid := lo + (hi-lo)/2
		size, err := estimatePaneHistoryMessageSizeWithEncoding(newPaneHistoryMessage(paneID, history[start:mid]), binaryPaneHistory)
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

func estimatePaneHistoryMessageSize(msg *Message) (int, error) {
	return estimatePaneHistoryMessageSizeWithEncoding(msg, false)
}

func estimatePaneHistoryMessageSizeWithEncoding(msg *Message, binaryPaneHistory bool) (int, error) {
	var buf bytes.Buffer
	writer := proto.NewWriter(&buf)
	writer.SetBinaryPaneHistory(binaryPaneHistory)
	if err := writer.WriteMsg(msg); err != nil {
		return 0, fmt.Errorf("encoding pane history message: %w", err)
	}
	return buf.Len(), nil
}
