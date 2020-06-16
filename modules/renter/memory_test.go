package renter

import (
	"testing"
)

// TestMemoryManager checks that the memory management is working correctly.
func TestMemoryManager(t *testing.T) {
	// Mimic the default parameters.
	stopChan := make(chan struct{})
	mm := newMemoryManager(100, 25, stopChan)

	// Low priority memory should have no issues requesting up to 75 memory.
	for i := 0; i < 75; i++ {
		if !mm.Request(1, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
	}

	// Request 1 more memory. This should not be allowed to complete until
	// memory has been returned.
	memoryCompleted1 := make(chan struct{})
	go func() {
		if !mm.Request(1, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted1)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.

	// Request some priority memory.
	for i := 0; i < 25; i++ {
		if !mm.Request(1, memoryPriorityHigh) {
			t.Error("unable to get memory")
		}
	}

	// Request 27 priority memory. This will consume all of the priority memory,
	// plus two slots that could go to the non-priority request. Because this is
	// a priority request, it should be granted first, even if there is enough
	// non-priority memory for the non-priority request.
	memoryCompleted2 := make(chan struct{})
	go func() {
		if !mm.Request(27, memoryPriorityHigh) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted2)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.

	// Return 26 memory, which should not be enough for either open request to
	// complete. The request for 1 will remain blocked because it is not allowed
	// to complete while there is an open priority request. The priority request
	// will not complete because there is not enough memory available.
	mm.Return(26)

	// Check that neither memory request has completed.
	select {
	case <-memoryCompleted1:
		t.Error("memory request should not have completed")
	case <-memoryCompleted2:
		t.Error("memory request should not have completed")
	default:
	}

	// Return 1 more memory. This should clear the priority request but not the
	// normal request.
	mm.Return(1)
	select {
	case <-memoryCompleted1:
		t.Error("memory request should not have completed")
	case <-memoryCompleted2:
	}

	// All memory is in use, return 26 memory so that there is room for this
	// request.
	mm.Return(26)
	<-memoryCompleted1

	// Try requesting a super large amount of memory on priority. This should
	// block all future requests until all memory has been returned.
	memoryCompleted3 := make(chan struct{})
	go func() {
		if !mm.Request(250, memoryPriorityHigh) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted3)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.
	// Create a couple of future requests, both priority and non priority.
	//
	// NOTE: We make the low priority requests first to ensure that the FIFO is
	// respecting priority.
	memoryCompleted6 := make(chan struct{})
	go func() {
		if !mm.Request(1, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted6)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.
	memoryCompleted7 := make(chan struct{})
	go func() {
		if !mm.Request(1, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted7)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.
	memoryCompleted4 := make(chan struct{})
	go func() {
		if !mm.Request(30, memoryPriorityHigh) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted4)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.
	memoryCompleted5 := make(chan struct{})
	go func() {
		if !mm.Request(1, memoryPriorityHigh) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted5)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.

	// Return 75 memory to get the mm back to zero, unblocking the big request.
	// All little requests should remain blocked.
	mm.Return(1)  // 1
	mm.Return(2)  // 3
	mm.Return(3)  // 6
	mm.Return(4)  // 10
	mm.Return(64) // 74

	// None of the memory requests should be able to complete.
	select {
	case <-memoryCompleted3:
		t.Error("memory should not complete")
	case <-memoryCompleted4:
		t.Error("memory should not complete")
	case <-memoryCompleted5:
		t.Error("memory should not complete")
	case <-memoryCompleted6:
		t.Error("memory should not complete")
	case <-memoryCompleted7:
		t.Error("memory should not complete")
	default:
	}

	// Return 1 more memory, this should unblock the big priority request.
	mm.Return(1)
	select {
	case <-memoryCompleted4:
		t.Error("memory should not complete")
	case <-memoryCompleted5:
		t.Error("memory should not complete")
	case <-memoryCompleted6:
		t.Error("memory should not complete")
	case <-memoryCompleted7:
		t.Error("memory should not complete")
	default:
	}

	// Return 150 memory, which means the large request is still holding the
	// full capacity of the mempool. None of the blocking threads should be
	// released. Because it is first in the fifo, nothing else should be
	// released either.
	mm.Return(1)  // 1
	mm.Return(2)  // 3
	mm.Return(3)  // 6
	mm.Return(4)  // 10
	mm.Return(65) // 75
	mm.Return(75) // 150
	select {
	case <-memoryCompleted4:
		t.Error("memory should not complete")
	case <-memoryCompleted5:
		t.Error("memory should not complete")
	case <-memoryCompleted6:
		t.Error("memory should not complete")
	case <-memoryCompleted7:
		t.Error("memory should not complete")
	default:
	}

	// Return 29 memory, which is not enough for the large request in the fifo
	// to be released.
	mm.Return(1)  // 1
	mm.Return(2)  // 3
	mm.Return(3)  // 6
	mm.Return(4)  // 10
	mm.Return(19) // 29
	select {
	case <-memoryCompleted4:
		t.Error("memory should not complete")
	case <-memoryCompleted5:
		t.Error("memory should not complete")
	case <-memoryCompleted6:
		t.Error("memory should not complete")
	case <-memoryCompleted7:
		t.Error("memory should not complete")
	default:
	}

	// Return 1 memory to release the large request.
	mm.Return(1)
	<-memoryCompleted4

	// Return 27 memory, which should be enough to let both the priority item
	// through as well as the first small memory item through. Needs to be +2
	// because the priority item takes the +1 away.
	mm.Return(27)
	// Check for memoryCompleted5
	select {
	case <-memoryCompleted5:
	case <-memoryCompleted7:
		t.Error("memory should not complete")
	}
	// Check for memoryCompleted6
	select {
	case <-memoryCompleted6:
	case <-memoryCompleted7:
		t.Error("memory should not complete")
	}

	// Return one more memory to clear that final request.
	mm.Return(1)
	<-memoryCompleted7

	// Do a check to make sure that large non priority requests do not block
	// priority requests.
	mm.Return(74) // There is still 1 memory unreturned.
	memoryCompleted8 := make(chan struct{})
	go func() {
		if !mm.Request(250, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted8)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.

	// Do some priority requests.
	if !mm.Request(10, memoryPriorityHigh) {
		t.Error("unable to get 10 memory")
	}
	if !mm.Request(5, memoryPriorityHigh) {
		t.Error("unable to get 10 memory")
	}
	if !mm.Request(20, memoryPriorityHigh) {
		t.Error("unable to get 10 memory")
	}
	// Clean up.
	mm.Return(36)
	<-memoryCompleted8
	mm.Return(250)
	if mm.available != mm.base {
		t.Error("test did not reset properly")
	}

	// Handle an edge case around awkwardly sized low priority memory requests.
	// The low priority request will go through.
	if !mm.Request(85, memoryPriorityLow) {
		t.Error("could not get memory")
	}
	memoryCompleted9 := make(chan struct{})
	go func() {
		if !mm.Request(20, memoryPriorityHigh) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted9)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.

	// The high priority request should not have been granted even though there
	// is enough high priority memory available, because the low priority
	// request was large enough to eat into the high priority memory.
	select {
	case <-memoryCompleted9:
		t.Error("memory request should not have gone through")
	default:
	}
	mm.Return(5)
	// Now that a small amount  of memory has been returned, the high priority
	// request should be able to complete.
	<-memoryCompleted9
	mm.Return(100)
	if mm.available != mm.base {
		t.Error("test did not reset properly")
	}
}
