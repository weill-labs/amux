package server

import (
	"errors"
	"sync"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
)

var errPacedInputClosed = errors.New("paced input queue closed")

const pacedInputRequestBufferSize = 256

type pacedInputRequest struct {
	chunks []encodedKeyChunk
	paneID uint32
	reply  chan error
}

// pacedInputQueue is a small actor that serializes delayed input writes.
// The actor owns all mutable queue state; callers only enqueue batches and wait
// for the batch result.
type pacedInputQueue struct {
	requests chan pacedInputRequest
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	logger   *charmlog.Logger
	write    func(uint32, []byte) error
	label    string
}

func newPacedInputQueue(label string, logger *charmlog.Logger, write func(uint32, []byte) error) *pacedInputQueue {
	if logger == nil {
		logger = auditlog.Discard()
	}
	q := &pacedInputQueue{
		requests: make(chan pacedInputRequest, pacedInputRequestBufferSize),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		logger:   logger,
		write:    write,
		label:    label,
	}
	go q.loop()
	return q
}

func (q *pacedInputQueue) enqueue(chunks []encodedKeyChunk) error {
	if len(chunks) == 0 {
		return nil
	}

	req := pacedInputRequest{
		chunks: cloneEncodedKeyChunks(chunks),
		reply:  make(chan error, 1),
	}
	return q.enqueueRequest(req)
}

func (q *pacedInputQueue) enqueueToPane(paneID uint32, chunks []encodedKeyChunk) error {
	if len(chunks) == 0 {
		return nil
	}

	req := pacedInputRequest{
		chunks: cloneEncodedKeyChunks(chunks),
		paneID: paneID,
		reply:  make(chan error, 1),
	}
	return q.enqueueRequest(req)
}

func (q *pacedInputQueue) enqueueRequest(req pacedInputRequest) error {
	select {
	case <-q.stop:
		return errPacedInputClosed
	case <-q.done:
		return errPacedInputClosed
	case q.requests <- req:
	}

	select {
	case err := <-req.reply:
		return err
	case <-q.done:
		select {
		case err := <-req.reply:
			return err
		default:
			return errPacedInputClosed
		}
	}
}

func (q *pacedInputQueue) enqueueAsync(chunks []encodedKeyChunk) error {
	if len(chunks) == 0 {
		return nil
	}

	req := pacedInputRequest{
		chunks: cloneEncodedKeyChunks(chunks),
	}
	return q.enqueueAsyncRequest(req)
}

func (q *pacedInputQueue) enqueueAsyncRequest(req pacedInputRequest) error {
	select {
	case <-q.stop:
		return errPacedInputClosed
	case <-q.done:
		return errPacedInputClosed
	case q.requests <- req:
		return nil
	}
}

func (q *pacedInputQueue) close() {
	q.stopOnce.Do(func() {
		close(q.stop)
	})
}

func (q *pacedInputQueue) loop() {
	defer close(q.done)

	for {
		select {
		case <-q.stop:
			return
		case req := <-q.requests:
			select {
			case <-q.stop:
				if req.reply != nil {
					req.reply <- errPacedInputClosed
				}
				return
			default:
			}

			err := q.writeBatch(req)
			if req.reply != nil {
				req.reply <- err
			}
			if err != nil {
				if !errors.Is(err, errPacedInputClosed) {
					q.logger.Warn("paced input failed",
						"event", "paced_input",
						"queue", q.label,
						"error", err,
					)
				}
				q.close()
				return
			}
		}
	}
}

func (q *pacedInputQueue) writeBatch(req pacedInputRequest) error {
	for i, chunk := range req.chunks {
		delay := time.Duration(0)
		switch {
		case chunk.delayBefore > 0:
			delay = chunk.delayBefore
		case i > 0 && chunk.paceBefore:
			delay = tokenKeyGap
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-q.stop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return errPacedInputClosed
			}
		}

		select {
		case <-q.stop:
			return errPacedInputClosed
		default:
		}

		if err := q.write(req.paneID, chunk.data); err != nil {
			return err
		}
	}
	return nil
}

func cloneEncodedKeyChunks(chunks []encodedKeyChunk) []encodedKeyChunk {
	cloned := make([]encodedKeyChunk, len(chunks))
	for i, chunk := range chunks {
		cloned[i] = encodedKeyChunk{
			data:        append([]byte(nil), chunk.data...),
			paceBefore:  chunk.paceBefore,
			delayBefore: chunk.delayBefore,
		}
	}
	return cloned
}
