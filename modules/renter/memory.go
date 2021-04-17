package renter

// TODO: Move the memory manager to its own package.

// TODO: Add functions that allow a caller to increase or decrease the base
// memory for the memory manager.

import (
	"container/list"
	"context"
	"sync"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
)

const (
	// memoryPriorityStarvationMultiple controls the amount of memory that needs
	// to be granted consecutively to high priority tasks before a low priority
	// task will be bumped into the high priority queue. For example, take a
	// multiple of 4 and a base memory of 1 GB. If 4 GB of memory is granted to
	// high priority tasks in a row, then this will trigger some of the low
	// priority tasks in the low priority queue to be bumped into the high
	// priority queue, ensuring that all tasks get access to memory even if
	// there is a continuous stream of high priority tasks.
	memoryPriorityStarvationMultiple = 4

	// memoryPriorityStarvationDivisor controls how many low priority items get
	// bumped into the high priority queue when starvation protection is
	// triggered. For example, take a divisor of 4 and a base memory of 1 GB.
	// When starvation is triggered, low priority items will be moved into the
	// high priority queue until a total of 250 MB or more of low priority items
	// have been added to the high priority queue.
	memoryPriorityStarvationDivisor = 4
)

// memoryManager can handle requests for memory and returns of memory. The
// memory manager is initialized with a base amount of memory and it will allow
// up to that much memory to be requested simultaneously. Beyond that, it will
// block on calls to 'managedGetMemory' until enough memory has been returned to
// allow the request. High priority memory will be unblocked first, otherwise
// memory will be unblocked in a FIFO.
//
// The memory manager will put aside 'priorityReserve' memory for high priority
// requests. Lower priority requests will not be able to use this memory. This
// allows high priority requests in low volume to experience zero wait time even
// if there are a high volume of low priority requests.
//
// If a request is made that exceeds the base memory, the memory manager will
// block until all memory is available, and then grant the request, blocking all
// future requests for memory until the memory is returned. This allows large
// requests to go through even if there is not enough base memory.
//
// The memoryManager keeps track of how much memory has been returned since the
// last manual call to runtime.GC(). After enough memory has been returned since
// the previous manual call, the memoryManager will run a manual call to
// runtime.GC() and follow that up with a call to debug.FreeOSMemory(). This has
// been shown in production to significantly reduce the amount of RES that siad
// consumes, without a significant hit to performance.
//
// Note that if a large low priority request comes in, it is possible for that
// large request to block higher priority requests because the memory manager
// will prefer to keep the memory footprint as close as possible to the
// initialized size rather than continue to allow high priority requests to go
// through when more than all of the memory has been used up.
//
// Note that there is a limited starvation prevention mechanism in place. If a
// large number of high priority requests are coming through, at a small ratio
// the lower priority requests will be bumped in priority.
type memoryManager struct {
	available           uint64 // Total memory remaining.
	base                uint64 // Initial memory.
	memSinceLowPriority uint64 // Counts allocations to bump low priority requests.
	priorityReserve     uint64 // Memory set aside for priority requests.
	underflow           uint64 // Large requests cause underflow.

	fifo         *memoryQueue
	priorityFifo *memoryQueue

	// The blocking channel receives a message (sent in a non-blocking way)
	// every time a request blocks for more memory. This is used in testing to
	// ensure that requests which are made in goroutines can be received in a
	// deterministic order.
	blocking chan struct{}
	mu       sync.Mutex
	stop     <-chan struct{}
}

// memoryRequest is a single thread that is blocked while waiting for memory.
type memoryRequest struct {
	amount   uint64
	canceled chan struct{}
	done     chan struct{}
}

// memoryQueue is a queue of memory requests.
type memoryQueue struct {
	*list.List
}

// newMemoryQueue initializes a new queue.
func newMemoryQueue() *memoryQueue {
	return &memoryQueue{
		List: list.New(),
	}
}

// Pop removes the first element of the queue.
func (queue *memoryQueue) Pop() *memoryRequest {
	mr := queue.Front()
	if mr == nil {
		return nil
	}
	return queue.List.Remove(mr).(*memoryRequest)
}

// handleStarvation will check whether high priority items have spent a
// significant amount of time blocking low priority items. If low priority items
// have not had a turn in a while, handleStarvation will bump a couple of low
// priority items into the high priority queue, to ensure that all tasks
// eventually get memory.
func (mm *memoryManager) handleStarvation() {
	// Unless there has been a long starvation period, do not bump any low
	// priority tasks.
	if mm.memSinceLowPriority < mm.base*memoryPriorityStarvationMultiple {
		return
	}
	// Bump a limited number of low priority items into the high priority queue.
	totalBumped := uint64(0)
	for totalBumped < mm.base/memoryPriorityStarvationDivisor && mm.fifo.Len() > 0 {
		req := mm.fifo.Pop()
		totalBumped += req.amount
		mm.priorityFifo.PushBack(req)
	}
	// Reset the starvation tracker.
	mm.memSinceLowPriority = 0
}

// try will try to get the amount of memory requested from the manger, returning
// true if the attempt is successful, and false if the attempt is not.  In the
// event that the attempt is successful, the internal state of the memory
// manager will be updated to reflect the granted request.
func (mm *memoryManager) try(amount uint64, priority bool) (success bool) {
	// Defer a function to check whether a low priority memory request has been
	// granted. If so, reset the starvation tracker.
	defer func() {
		if success && !priority {
			mm.memSinceLowPriority = 0
		}
	}()

	// If there is enough memory available, then the request can be granted. For
	// non-priority memory, we compare the amount available to the amount being
	// requested plus the priority reserve to ensure that the total amount of
	// memory left is more than the priority reserve when the requested amount
	// is subtracted. For priority memory, we only check that the amount
	// requested is less than the total amount available.
	//
	// If the request is larger than the total amount of memory that the memory
	// manager is allowed to pass out, then the request will be granted only if
	// all of the memory is available.
	if mm.available >= (amount+mm.priorityReserve) || (priority && mm.available >= amount) {
		// There is enough memory, decrement the memory and return.
		mm.available -= amount
		return true
	} else if mm.available == mm.base {
		// The amount of memory being requested is greater than the amount of
		// memory available, but no memory is currently in use. Note that edge
		// cases around the priority memory limit need to be respected - if this
		// is a low priority request it may not consume the entire set of
		// available memory.
		if amount <= mm.available {
			mm.available -= amount
		} else {
			mm.available = 0
			mm.underflow = amount - mm.base
		}
		return true
	}
	return false
}

// Request is a blocking request for memory. The request will return
// when the memory has been acquired, or when the given context gets canceled.
// If 'false' is returned, it means that the function returned before the memory
// could be allocated.
func (mm *memoryManager) Request(ctx context.Context, amount uint64, priority bool) bool {
	// If this is a priority request and the low priority fifo is not empty,
	// increment the starvation tracker, because either this request will be
	// granted or this request will be put in the queue to fire ahead of any low
	// priority request currently in the queue.
	mm.mu.Lock()
	if priority && mm.fifo.Len() != 0 {
		mm.memSinceLowPriority += amount
		mm.handleStarvation()
	}
	// Try to request the memory.
	shouldTry := mm.priorityFifo.Len() == 0 && (priority || mm.fifo.Len() == 0)
	if shouldTry && mm.try(amount, priority) {
		mm.mu.Unlock()
		return true
	}
	// There is not enough memory available for this request, join the fifo.
	myRequest := &memoryRequest{
		amount:   amount,
		canceled: make(chan struct{}),
		done:     make(chan struct{}),
	}

	// Keep track of the list element so we remove it in case we time out
	var el *list.Element
	if priority {
		el = mm.priorityFifo.PushBack(myRequest)
	} else {
		el = mm.fifo.PushBack(myRequest)
	}
	mm.mu.Unlock()

	// Send a note that a thread is now blocking. This is only used in testing,
	// to ensure that the test can have multiple threads blocking for memory
	// which block in a determinstic order.
	if build.Release == "testing" {
		select {
		case mm.blocking <- struct{}{}:
		default:
		}
	}

	// Block until memory is available or until shutdown/timeout. The thread
	// that closes the 'available' channel will also handle updating the
	// memoryManager variables.
	select {
	case <-myRequest.done:
		return true
	case <-ctx.Done():
		close(myRequest.canceled)

		// Try and remove the element from the queue, this is pure cosmetical
		// and will make sure the requested memory in the MemoryStatus more
		// accurately reflects the actual memory being requested still. There's
		// an edge case where the element has been moved to the priority queue
		// due to the starvation code, in which case the remove here won't be
		// successful.
		mm.mu.Lock()
		if priority {
			mm.priorityFifo.Remove(el)
		} else {
			mm.fifo.Remove(el)
		}
		mm.mu.Unlock()
		return false
	case <-mm.stop:
		return false
	}
}

// Return will return memory to the manager, waking any blocking threads which
// now have enough memory to proceed.
func (mm *memoryManager) Return(amount uint64) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	// Add the remaining memory to the pool of available memory, clearing out
	// the underflow if needed.
	if mm.underflow > 0 && amount <= mm.underflow {
		// Not even enough memory has been returned to clear the underflow.
		// Reduce the underflow amount and return.
		mm.underflow -= amount
		return
	} else if mm.underflow > 0 && amount > mm.underflow {
		amount -= mm.underflow
		mm.underflow = 0
	}
	mm.available += amount

	// Sanity check - the amount of memory available should not exceed the base
	// unless the memory manager is being used incorrectly.
	if mm.available > mm.base {
		build.Critical("renter memory manager being used incorrectly, too much memory returned")
		mm.available = mm.base
	}

	// Release as many of the priority threads blocking in the fifo as possible.
	for mm.priorityFifo.Len() > 0 {
		req := mm.priorityFifo.Pop()

		// Check whether the request got canceled, if so ignore it and continue
		select {
		case <-req.canceled:
			continue
		default:
		}

		// Check whether the starvation tracker thinks that low priority
		// requests should be bumped to the high priority queue. This is done to
		// prevent the high priority requests from fully starving the low
		// priority requests.
		if !mm.try(req.amount, memoryPriorityHigh) {
			// There is not enough memory to grant the next request, meaning no
			// future requests should be checked either.
			mm.priorityFifo.PushFront(req)
			return
		}
		// There is enough memory to grant the next request. Unblock that
		// request and continue checking the next requests.
		close(req.done)
	}

	// Release as many of the threads blocking in the fifo as possible.
	for mm.fifo.Len() > 0 {
		req := mm.fifo.Pop()

		// Check whether the request got canceled, if so ignore it and continue
		select {
		case <-req.canceled:
			continue
		default:
		}

		if !mm.try(req.amount, memoryPriorityLow) {
			// There is not enough memory to grant the next request, meaning no
			// future requests should be checked either.
			mm.fifo.PushFront(req)
			return
		}
		// There is enough memory to grant the next request. Unblock that
		// request and continue checking the next requests.
		close(req.done)
	}
}

// callAvailable returns the current status of the memory manager.
func (mm *memoryManager) callStatus() modules.MemoryManagerStatus {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	var available, requested, priorityAvailable, priorityRequested uint64

	// Determine available memory and priority memory. All memory is available
	// as priority memory. If there is more memory available than the amount
	// reserved for priority memory then there is also regular memory
	// availability.
	priorityAvailable = mm.available
	if mm.available > mm.priorityReserve {
		available = mm.available - mm.priorityReserve
	}

	// Calculate how much memory has been requested for each
	for ele := mm.fifo.Front(); ele != nil; ele = ele.Next() {
		req := ele.Value.(*memoryRequest)
		requested += req.amount
	}
	for ele := mm.priorityFifo.Front(); ele != nil; ele = ele.Next() {
		req := ele.Value.(*memoryRequest)
		priorityRequested += req.amount
	}

	return modules.MemoryManagerStatus{
		Available: available,
		Base:      mm.base - mm.priorityReserve,
		Requested: requested,

		PriorityAvailable: priorityAvailable,
		PriorityBase:      mm.base,
		PriorityRequested: priorityRequested,
		PriorityReserve:   mm.priorityReserve,
	}
}

// newMemoryManager will create a memoryManager and return it.
func newMemoryManager(baseMemory uint64, priorityMemory uint64, stopChan <-chan struct{}) *memoryManager {
	return &memoryManager{
		available:       baseMemory,
		base:            baseMemory,
		priorityReserve: priorityMemory,

		fifo:         newMemoryQueue(),
		priorityFifo: newMemoryQueue(),

		blocking: make(chan struct{}, 1),
		stop:     stopChan,
	}
}
