package server

import (
	"errors"
	"log"
	"sync"
	"time"
)

var errPacedInputClosed = errors.New("paced input queue closed")

const pacedInputRequestBufferSize = 256

type pacedInputRequest struct {
	chunks []encodedKeyChunk
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
	write    func([]byte) error
	label    string
}

func newPacedInputQueue(label string, write func([]byte) error) *pacedInputQueue {
	q := &pacedInputQueue{
		requests: make(chan pacedInputRequest, pacedInputRequestBufferSize),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
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

			err := q.writeBatch(req.chunks)
			if req.reply != nil {
				req.reply <- err
			}
			if err != nil {
				if !errors.Is(err, errPacedInputClosed) {
					log.Printf("[amux] paced input %s: %v", q.label, err)
				}
				q.close()
				return
			}
		}
	}
}

func (q *pacedInputQueue) writeBatch(chunks []encodedKeyChunk) error {
	for i, chunk := range chunks {
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

		if err := q.write(chunk.data); err != nil {
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
