package server

import (
	"errors"
	"testing"
	"time"
)

func TestPacedInputQueueWaitsForFullBatch(t *testing.T) {
	t.Parallel()

	secondWriteBlocked := make(chan struct{}, 1)
	releaseSecondWrite := make(chan struct{})
	writes := make(chan []byte, 2)

	q := newPacedInputQueue("test", func(_ uint32, data []byte) error {
		copyData := append([]byte(nil), data...)
		writes <- copyData
		if string(data) == "\r" {
			secondWriteBlocked <- struct{}{}
			<-releaseSecondWrite
		}
		return nil
	})
	defer q.close()

	done := make(chan error, 1)
	go func() {
		done <- q.enqueue([]encodedKeyChunk{
			{data: []byte("HELLO")},
			{data: []byte{'\r'}, paceBefore: true},
		})
	}()

	if got := <-writes; string(got) != "HELLO" {
		t.Fatalf("first write = %q, want %q", got, "HELLO")
	}
	select {
	case err := <-done:
		t.Fatalf("enqueue returned before second chunk started: %v", err)
	default:
	}

	if got := <-writes; string(got) != "\r" {
		t.Fatalf("second write = %q, want carriage return", got)
	}
	<-secondWriteBlocked

	select {
	case err := <-done:
		t.Fatalf("enqueue returned before second chunk completed: %v", err)
	default:
	}

	close(releaseSecondWrite)
	if err := <-done; err != nil {
		t.Fatalf("enqueue returned error: %v", err)
	}
}

func TestPacedInputQueueCloseAbortsPendingBatch(t *testing.T) {
	t.Parallel()

	firstWrite := make(chan struct{}, 1)
	secondWrite := make(chan struct{}, 1)

	q := newPacedInputQueue("test", func(_ uint32, data []byte) error {
		if string(data) == "HELLO" {
			firstWrite <- struct{}{}
			return nil
		}
		secondWrite <- struct{}{}
		return nil
	})
	defer q.close()

	done := make(chan error, 1)
	go func() {
		done <- q.enqueue([]encodedKeyChunk{
			{data: []byte("HELLO")},
			{data: []byte{'\r'}, paceBefore: true},
		})
	}()

	<-firstWrite
	q.close()

	err := <-done
	if !errors.Is(err, errPacedInputClosed) {
		t.Fatalf("enqueue error = %v, want %v", err, errPacedInputClosed)
	}

	select {
	case <-secondWrite:
		t.Fatal("queue should not write the second chunk after close")
	default:
	}
}

func TestPacedInputQueueAsyncReturnsBeforeBlockedWriteCompletes(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	writes := make(chan []byte, 3)

	q := newPacedInputQueue("test", func(_ uint32, data []byte) error {
		copyData := append([]byte(nil), data...)
		writes <- copyData
		if string(data) == "first" {
			<-release
		}
		return nil
	})
	defer q.close()

	if err := q.enqueueAsync([]encodedKeyChunk{{data: []byte("first")}}); err != nil {
		t.Fatalf("enqueueAsync(first) = %v", err)
	}

	doneAsync := make(chan error, 1)
	go func() {
		doneAsync <- q.enqueueAsync([]encodedKeyChunk{{data: []byte("second")}})
	}()

	select {
	case err := <-doneAsync:
		if err != nil {
			t.Fatalf("enqueueAsync(second) = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("enqueueAsync(second) blocked on an earlier write")
	}

	doneWait := make(chan error, 1)
	go func() {
		doneWait <- q.enqueue([]encodedKeyChunk{{data: []byte("third")}})
	}()

	if got := <-writes; string(got) != "first" {
		t.Fatalf("first write = %q, want %q", got, "first")
	}

	select {
	case err := <-doneWait:
		t.Fatalf("enqueue(third) returned before blocked write was released: %v", err)
	default:
	}

	close(release)

	if got := <-writes; string(got) != "second" {
		t.Fatalf("second write = %q, want %q", got, "second")
	}
	if got := <-writes; string(got) != "third" {
		t.Fatalf("third write = %q, want %q", got, "third")
	}

	if err := <-doneWait; err != nil {
		t.Fatalf("enqueue(third) = %v", err)
	}
}
