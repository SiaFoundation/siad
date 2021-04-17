package renter

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
)

// TestMemoryManager checks that the memory management is working correctly.
func TestMemoryManager(t *testing.T) {
	// Mimic the default parameters.
	stopChan := make(chan struct{})
	mm := newMemoryManager(100, 25, stopChan)

	// Low priority memory should have no issues requesting up to 75 memory.
	for i := 0; i < 75; i++ {
		if !mm.Request(context.Background(), 1, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
	}

	// Request 1 more memory. This should not be allowed to complete until
	// memory has been returned.
	memoryCompleted1 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 1, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted1)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.

	// Request some priority memory.
	for i := 0; i < 25; i++ {
		if !mm.Request(context.Background(), 1, memoryPriorityHigh) {
			t.Error("unable to get memory")
		}
	}

	// Request 27 priority memory. This will consume all of the priority memory,
	// plus two slots that could go to the non-priority request. Because this is
	// a priority request, it should be granted first, even if there is enough
	// non-priority memory for the non-priority request.
	memoryCompleted2 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 27, memoryPriorityHigh) {
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
		if !mm.Request(context.Background(), 250, memoryPriorityHigh) {
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
		if !mm.Request(context.Background(), 1, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted6)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.
	memoryCompleted7 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 1, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted7)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.
	memoryCompleted4 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 30, memoryPriorityHigh) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted4)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.
	memoryCompleted5 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 1, memoryPriorityHigh) {
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
		if !mm.Request(context.Background(), 250, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted8)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.

	// Do some priority requests.
	if !mm.Request(context.Background(), 10, memoryPriorityHigh) {
		t.Error("unable to get 10 memory")
	}
	if !mm.Request(context.Background(), 5, memoryPriorityHigh) {
		t.Error("unable to get 10 memory")
	}
	if !mm.Request(context.Background(), 20, memoryPriorityHigh) {
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
	if !mm.Request(context.Background(), 85, memoryPriorityLow) {
		t.Error("could not get memory")
	}
	memoryCompleted9 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 20, memoryPriorityHigh) {
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

	// Test out the starvation detector. Request a continuout stream of high
	// priority memory that should starve out the low priority memory. The
	// starvation detector should make sure that eventually, the low priority
	// memory is able to make progress.
	if !mm.Request(context.Background(), 100, memoryPriorityHigh) {
		t.Error("could not get memory through")
	}
	// Add 3 low priority requests each for 10 memory. All 3 should be unblocked
	// by the starvation detector at the same time.
	memoryCompleted10 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 10, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted10)
	}()
	<-mm.blocking
	memoryCompleted11 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 10, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted11)
	}()
	<-mm.blocking
	memoryCompleted12 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 10, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted12)
	}()
	<-mm.blocking
	// Add another low priority request, this should be unblocked by the
	// starvation detector much later than the previous 3.
	memoryCompleted13 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 30, memoryPriorityLow) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted13)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.

	// Add high priority requests and release previous high priority items.
	// These should all unblock as soon as memory is returned.
	for i := 0; i < 3; i++ {
		memoryCompletedL := make(chan struct{})
		go func() {
			if !mm.Request(context.Background(), 100, memoryPriorityHigh) {
				t.Error("unable to get memory")
			}
			close(memoryCompletedL)
		}()
		<-mm.blocking // wait until the goroutine is in the fifo.
		mm.Return(100)
		<-memoryCompletedL
	}

	// Add a high priority request. The next time memory is returned, the first
	// set of low priority items should go through.
	memoryCompleted14 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 100, memoryPriorityHigh) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted14)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.
	mm.Return(100)
	// First set of low priority requests should have gone through.
	<-memoryCompleted10
	<-memoryCompleted11
	<-memoryCompleted12
	// Second set should not have gone through.
	select {
	case <-memoryCompleted13:
		t.Error("memory should not have been released")
	default:
	}
	mm.Return(30)
	<-memoryCompleted14

	// Add high priority requests and release previous high priority items.
	// These should all unblock as soon as memory is returned.
	for i := 0; i < 3; i++ {
		memoryCompletedL := make(chan struct{})
		go func() {
			if !mm.Request(context.Background(), 100, memoryPriorityHigh) {
				t.Error("unable to get memory")
			}
			close(memoryCompletedL)
		}()
		<-mm.blocking // wait until the goroutine is in the fifo.
		mm.Return(100)
		<-memoryCompletedL

		// Second set should not have gone through still.
		select {
		case <-memoryCompleted13:
			t.Error("memory should not have been released")
		default:
		}
	}
	memoryCompleted15 := make(chan struct{})
	go func() {
		if !mm.Request(context.Background(), 100, memoryPriorityHigh) {
			t.Error("unable to get memory")
		}
		close(memoryCompleted15)
	}()
	<-mm.blocking // wait until the goroutine is in the fifo.
	mm.Return(100)
	// Second set of low priority requests should have gone through.
	<-memoryCompleted13
	mm.Return(30)
	<-memoryCompleted15
	mm.Return(100)
	if mm.available != mm.base {
		t.Error("test did not reset properly")
	}
}

// TestMemoryManager checks that the memory management is working correctly.
func TestMemoryManagerConcurrent(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Mimic the default parameters.
	stopChan := make(chan struct{})
	mm := newMemoryManager(100, 25, stopChan)

	// Spin up a bunch of threads to all request and release memory at the same
	// time.
	doMemory := func() {
		for {
			// Check if the thread has been killed.
			select {
			case <-stopChan:
				return
			default:
			}

			// Randomly request some amount of memory. Sometimes there will be
			// overdrafts.
			memNeeded := uint64(fastrand.Intn(110) + 1)
			// Randomly set the priority of this memory.
			priority := false
			if fastrand.Intn(2) == 0 {
				priority = true
			}

			// Perform the request.
			if !mm.Request(context.Background(), memNeeded, priority) {
				select {
				case <-stopChan:
					return
				default:
					t.Error("request failed even though the mm hasn't been shut down")
				}
				return
			}

			// Sit on the memory for some random (low) number of microseconds.
			sleepTime := time.Microsecond * time.Duration(fastrand.Intn(1e3))
			time.Sleep(sleepTime)

			// Randomly decide whether to return all of the memory at once.
			if fastrand.Intn(2) == 0 {
				mm.Return(memNeeded)
				continue
			}
			// Return random smaller amounts of memory.
			for memNeeded > 0 {
				returnAmt := uint64(fastrand.Intn(int(memNeeded))) + 1
				memNeeded -= returnAmt
				mm.Return(uint64(returnAmt))
			}
		}
	}

	// Spin up 20 threads to compete for memory.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			doMemory()
			wg.Done()
		}()
	}

	// Sleep for 10 seconds to let the threads do their thing.
	time.Sleep(time.Second * 10)

	// Close out the memory and wait for all the threads to die.
	close(stopChan)
	wg.Wait()
}

// TestMemoryManagerStatus probes the response from callStatus
func TestMemoryManagerStatus(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create memory manager
	memoryDefault := repairMemoryDefault
	memoryPriorityDefault := memoryDefault / 4
	stopChan := make(chan struct{})
	mm := newMemoryManager(memoryDefault, memoryPriorityDefault, stopChan)

	// Check status
	ms := mm.callStatus()
	expectedStatus := modules.MemoryManagerStatus{
		Available: memoryDefault - memoryPriorityDefault,
		Base:      memoryDefault - memoryPriorityDefault,
		Requested: 0,

		PriorityAvailable: memoryDefault,
		PriorityBase:      memoryDefault,
		PriorityRequested: 0,
		PriorityReserve:   memoryPriorityDefault,
	}
	if !reflect.DeepEqual(ms, expectedStatus) {
		t.Log("Expected:", expectedStatus)
		t.Log("Status:", ms)
		t.Fatal("MemoryStatus not as expected")
	}

	// Request memory
	normalRequest := uint64(100)
	requested := mm.Request(context.Background(), normalRequest, memoryPriorityLow)
	if !requested {
		t.Error("Normal request should have succeeded")
	}
	priorityRequest := uint64(123)
	requested = mm.Request(context.Background(), priorityRequest, memoryPriorityHigh)
	if !requested {
		t.Error("Priority request should have succeeded")
	}

	// Check status
	ms = mm.callStatus()
	expectedStatus = modules.MemoryManagerStatus{
		Available: memoryDefault - memoryPriorityDefault - normalRequest - priorityRequest,
		Base:      memoryDefault - memoryPriorityDefault,
		Requested: 0,

		PriorityAvailable: memoryDefault - normalRequest - priorityRequest,
		PriorityBase:      memoryDefault,
		PriorityRequested: 0,
		PriorityReserve:   memoryPriorityDefault,
	}
	if !reflect.DeepEqual(ms, expectedStatus) {
		t.Log("Expected:", expectedStatus)
		t.Log("Status:", ms)
		t.Fatal("MemoryStatus not as expected")
	}

	// Request remaining memory
	mm.mu.Lock()
	request := mm.available
	mm.mu.Unlock()
	requested = mm.Request(context.Background(), request, memoryPriorityHigh)
	if !requested {
		t.Error("Priority request should have succeeded")
	}

	// Check status
	ms = mm.callStatus()
	expectedStatus = modules.MemoryManagerStatus{
		Available: 0,
		Base:      memoryDefault - memoryPriorityDefault,
		Requested: 0,

		PriorityAvailable: 0,
		PriorityBase:      memoryDefault,
		PriorityRequested: 0,
		PriorityReserve:   memoryPriorityDefault,
	}
	if !reflect.DeepEqual(ms, expectedStatus) {
		t.Log("Expected:", expectedStatus)
		t.Log("Status:", ms)
		t.Fatal("MemoryStatus not as expected")
	}

	// Request enough memory to have a FIFO queue.
	//
	// These must happen in a go routine since they are blocking calls. We don't
	// care about the calls returning since the block is after the request is
	// added to the FIFO queue which is what the test is concerned with.
	go func() {
		_ = mm.Request(context.Background(), memoryDefault, memoryPriorityLow)
	}()
	go func() {
		_ = mm.Request(context.Background(), memoryDefault, memoryPriorityHigh)
	}()

	// Since the requests are being handled in a go routine, wait until each
	// request appears in the FIFO queue
	err := build.Retry(100, 10*time.Millisecond, func() error {
		mm.mu.Lock()
		defer mm.mu.Unlock()
		if mm.fifo.Len() != 1 {
			return fmt.Errorf("FIFO queue should have 1 request but has %v", mm.fifo.Len())
		}
		if mm.priorityFifo.Len() != 1 {
			return fmt.Errorf("Priority FIFO queue should have 1 request but has %v", mm.priorityFifo.Len())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Check Status
	ms = mm.callStatus()
	expectedStatus = modules.MemoryManagerStatus{
		Available: 0,
		Base:      memoryDefault - memoryPriorityDefault,
		Requested: memoryDefault,

		PriorityAvailable: 0,
		PriorityBase:      memoryDefault,
		PriorityRequested: memoryDefault,
		PriorityReserve:   memoryPriorityDefault,
	}
	if !reflect.DeepEqual(ms, expectedStatus) {
		t.Log("Expected:", expectedStatus)
		t.Log("Status:", ms)
		t.Fatal("MemoryStatus not as expected")
	}
}

// TestMemoryManagerRequestMemoryWithContext verifies the behaviour of
// RequestWithContext method on the memory manager
func TestMemoryManagerRequestMemoryWithContext(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create memory manager
	stopChan := make(chan struct{})
	mm := newMemoryManager(repairMemoryDefault, repairMemoryPriorityDefault, stopChan)

	// Get the total available memory
	mm.mu.Lock()
	available := mm.available
	mm.mu.Unlock()

	// Request all available memory
	requested := mm.Request(context.Background(), available, memoryPriorityHigh)
	if !requested {
		t.Fatal("Priority request should have succeeded")
	}

	// Validate that requesting more memory blocks
	doneChan := make(chan struct{})
	go func() {
		mm.Request(context.Background(), 1, memoryPriorityHigh)
		close(doneChan)
	}()
	select {
	case <-doneChan:
		t.Fatal("Priority request should have been blocking...")
	case <-time.After(time.Second):
	}

	// Validate the current status
	status := mm.callStatus()
	if status.PriorityAvailable != 0 || status.PriorityRequested != 1 {
		t.Fatal("unexpected")
	}

	// Return some memory, this should unblock the previously queued request
	mm.Return(1)
	select {
	case <-time.After(time.Second):
		t.Fatal("Request should have been unblocked now")
	case <-doneChan:
	}

	// Validate the current status
	status = mm.callStatus()
	if status.PriorityAvailable != 0 || status.PriorityRequested != 0 {
		t.Fatal("unexpected")
	}

	// Request some memory, this time pass a context that times out
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	requested = mm.Request(ctx, 10, memoryPriorityHigh)
	if requested {
		t.Fatal("Priority request should have timed out")
	}

	// Validate the current status
	status = mm.callStatus()
	if status.PriorityAvailable != 0 || status.PriorityRequested != 0 {
		t.Fatal("unexpected")
	}

	// Return all available memory
	mm.Return(available)
	status = mm.callStatus()
	if status.PriorityAvailable != available {
		t.Fatal("unexpected")
	}
}

// TestAddMemoryStatus is a unit test for adding up MemoryStatus objects.
func TestAddMemoryStatus(t *testing.T) {
	mms := modules.MemoryManagerStatus{
		Available: 1,
		Base:      2,
		Requested: 3,

		PriorityAvailable: 4,
		PriorityBase:      5,
		PriorityRequested: 6,
		PriorityReserve:   7,
	}
	total := mms.Add(mms)

	if total.Available != 2*mms.Available {
		t.Fatal("invalid")
	}
	if total.Base != 2*mms.Base {
		t.Fatal("invalid")
	}
	if total.Requested != 2*mms.Requested {
		t.Fatal("invalid")
	}
	if total.PriorityAvailable != 2*mms.PriorityAvailable {
		t.Fatal("invalid")
	}
	if total.PriorityBase != 2*mms.PriorityBase {
		t.Fatal("invalid")
	}
	if total.PriorityRequested != 2*mms.PriorityRequested {
		t.Fatal("invalid")
	}
	if total.PriorityReserve != 2*mms.PriorityReserve {
		t.Fatal("invalid")
	}
}
