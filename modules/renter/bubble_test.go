package renter

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/modules"
)

var (
	// bubbleWaitInTestTime is the amount of time a test should wait before
	// returning an error in test. This is a conservative value to prevent test
	// timeouts.
	bubbleWaitInTestTime = time.Minute
)

// bubble is a helper for the renterTester to call bubble on a directory and
// block until the bubble has executed.
func (rt *renterTester) bubble(siaPath modules.SiaPath) error {
	complete := rt.renter.staticBubbleScheduler.callQueueBubble(siaPath)
	select {
	case <-complete:
	case <-time.After(bubbleWaitInTestTime):
		return errors.New("test blocked too long for bubble")
	}
	return nil
}

// bubbleAll is a helper for the renterTester to call bubble on multiple
// directories and block until all the bubbles has executed.
func (rt *renterTester) bubbleAll(siaPaths []modules.SiaPath) (errs error) {
	// Define common variables
	var errMU sync.Mutex
	siaPathChan := make(chan modules.SiaPath, numBubbleWorkerThreads)
	var wg sync.WaitGroup

	// Define bubbleWorker to call bubble on siaPaths
	bubbleWorker := func() {
		for siaPath := range siaPathChan {
			err := rt.bubble(siaPath)
			errMU.Lock()
			errs = errors.Compose(errs, err)
			errMU.Unlock()
		}
	}

	// Launch bubble workers
	for i := 0; i < numBubbleWorkerThreads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bubbleWorker()
		}()
	}

	// Send siaPaths to bubble workers over the siaPathChan
	for _, siaPath := range siaPaths {
		// renterTester bubble method has timeout protection so no need for it here
		siaPathChan <- siaPath
	}
	return
}

// TestBubble tests the bubble code.
//
// TODO: moves bubble tests from other files into here
func TestBubble(t *testing.T) {
	t.Parallel()

	// bubbleQueue unit tests
	t.Run("BubbleQueue", testBubbleQueue)

	// bubbleScheduler unit tests
	t.Run("BubbleScheduler", testBubbleScheduler)
}

// testBubbleQueue probes the bubbleQueue
func testBubbleQueue(t *testing.T) {
	// Initialize a queue
	bq := newBubbleQueue()

	// Calling pop should result in nil because it is empty
	bu := bq.Pop()
	if bu != nil {
		t.Error("expected nil bubble update")
	}

	// Add a bubble update to the queue
	newBU := &bubbleUpdate{
		staticSiaPath: modules.RandomSiaPath(),
		status:        bubbleQueued,
	}
	bq.Push(newBU)

	// Pop should return the bubble update just pushed
	bu = bq.Pop()
	if bu == nil {
		t.Fatal("nil bubble update popped")
	}
	if !reflect.DeepEqual(newBU, bu) {
		t.Log("found", bu)
		t.Log("expected", newBU)
		t.Error("Popped bubble update unexpected")
	}

	// Another call to Pop should return nil
	bu = bq.Pop()
	if bu != nil {
		t.Error("expected nil bubble update")
	}

	// Push the bubble Update back to the queue
	bq.Push(newBU)

	// Push a few more updates
	for i := 0; i < 5; i++ {
		bq.Push(&bubbleUpdate{
			staticSiaPath: modules.RandomSiaPath(),
			status:        bubbleQueued,
		})
	}

	// Pop should return the first update pushed
	bu = bq.Pop()
	if bu == nil {
		t.Fatal("nil bubble update popped")
	}
	if !reflect.DeepEqual(newBU, bu) {
		t.Log("found", bu)
		t.Log("expected", newBU)
		t.Error("Popped bubble update unexpected")
	}
}

// testBubbleScheduler probes the bubbleScheduler
func testBubbleScheduler(t *testing.T) {
	// Basic functionality test
	t.Run("Basic", testBubbleScheduler_Basic)

	// Specific Methods
	t.Run("managedQueueParent", testBubbleScheduler_managedQueueParent)

	if testing.Short() {
		t.SkipNow()
	}

	// Blocking functionality test
	t.Run("Blocking", testBubbleScheduler_Blocking)
}

// testBubbleScheduler_Basic probes the basic functionality of the
// bubbleScheduler
func testBubbleScheduler_Basic(t *testing.T) {
	// Initialize a bubble scheduler
	bs := newBubbleScheduler(&Renter{})

	// Check status
	bs.mu.Lock()
	fifoSize := bs.fifo.Len()
	mapSize := len(bs.bubbleUpdates)
	bs.mu.Unlock()
	if fifoSize != 0 || mapSize != 0 {
		t.Error("unexpected", fifoSize, mapSize)
	}

	// managedPop should return nil
	bu := bs.managedPop()
	if bu != nil {
		t.Error("expected nil bubble update")
	}

	// checkStatus is a helper function to check the status of the bubble update
	checkStatus := func(siaPath modules.SiaPath, status bubbleStatus, checkQueue bool) {
		// Map and queue should both contain the same update
		bu, ok := bs.bubbleUpdates[siaPath]
		if !ok {
			t.Fatal("bad")
		}
		if !bu.staticSiaPath.Equals(siaPath) {
			t.Log("found", bu.staticSiaPath)
			t.Log("expected", siaPath)
			t.Error("incorrect siaPath found")
		}
		if bu.status != status {
			t.Log("found", bu.status)
			t.Log("expected", status)
			t.Error("incorrect status found")
		}

		if !checkQueue {
			return
		}

		// Check the queue
		queuedBU := bs.fifo.Pop()
		if !reflect.DeepEqual(bu, queuedBU) {
			t.Log("map BU", bu)
			t.Log("queue BU", queuedBU)
			t.Error("map and queue have different bubble updates")
		}

		// Push the update back to the queue
		bs.fifo.Push(queuedBU)
	}

	// queue a bubble update request
	siaPath := modules.RandomSiaPath()
	_ = bs.callQueueBubble(siaPath)

	// Check the status
	checkStatus(siaPath, bubbleQueued, true)

	// Check status
	bs.mu.Lock()
	fifoSize = bs.fifo.Len()
	mapSize = len(bs.bubbleUpdates)
	bs.mu.Unlock()
	if fifoSize != 1 || mapSize != 1 {
		t.Error("unexpected", fifoSize, mapSize)
	}

	// Calling queue again should have no impact
	_ = bs.callQueueBubble(siaPath)
	checkStatus(siaPath, bubbleQueued, true)

	// Check status
	bs.mu.Lock()
	fifoSize = bs.fifo.Len()
	mapSize = len(bs.bubbleUpdates)
	bs.mu.Unlock()
	if fifoSize != 1 || mapSize != 1 {
		t.Error("unexpected", fifoSize, mapSize)
	}

	// managedPop should return the bubble update with the status now set to
	// bubbleActive
	bu = bs.managedPop()
	checkStatus(siaPath, bubbleActive, false)

	// Queue should be empty but map should still have update
	bs.mu.Lock()
	fifoSize = bs.fifo.Len()
	mapSize = len(bs.bubbleUpdates)
	bs.mu.Unlock()
	if fifoSize != 0 || mapSize != 1 {
		t.Error("unexpected", fifoSize, mapSize)
	}

	// Calling queue should update the status to pending
	_ = bs.callQueueBubble(siaPath)
	checkStatus(siaPath, bubblePending, false)

	// Queue should be empty but map should still have update
	bs.mu.Lock()
	fifoSize = bs.fifo.Len()
	mapSize = len(bs.bubbleUpdates)
	bs.mu.Unlock()
	if fifoSize != 0 || mapSize != 1 {
		t.Error("unexpected", fifoSize, mapSize)
	}

	// Calling queue again should have no impact
	_ = bs.callQueueBubble(siaPath)
	checkStatus(siaPath, bubblePending, false)

	// Queue should be empty but map should still have update
	bs.mu.Lock()
	fifoSize = bs.fifo.Len()
	mapSize = len(bs.bubbleUpdates)
	bs.mu.Unlock()
	if fifoSize != 0 || mapSize != 1 {
		t.Error("unexpected", fifoSize, mapSize)
	}

	// Calling complete should add the update back to the queue
	bs.managedCompleteBubbleUpdate(siaPath)
	checkStatus(siaPath, bubbleQueued, true)

	// Check Status
	bs.mu.Lock()
	fifoSize = bs.fifo.Len()
	mapSize = len(bs.bubbleUpdates)
	bs.mu.Unlock()
	if fifoSize != 1 || mapSize != 1 {
		t.Error("unexpected", fifoSize, mapSize)
	}

	// calling managed pop and then calling complete should result in an empty map
	// and queue
	_ = bs.managedPop()
	bs.managedCompleteBubbleUpdate(siaPath)

	// Check Status
	bs.mu.Lock()
	fifoSize = bs.fifo.Len()
	mapSize = len(bs.bubbleUpdates)
	bs.mu.Unlock()
	if fifoSize != 0 || mapSize != 0 {
		t.Error("unexpected", fifoSize, mapSize)
	}
}

// testBubbleScheduler_Blocking probes the blocking nature of the complete
// channel in the bubble update
func testBubbleScheduler_Blocking(t *testing.T) {
	// Initialize a bubble scheduler
	bs := newBubbleScheduler(&Renter{})

	// queue a bubble update request
	siaPath := modules.RandomSiaPath()
	completeChan := bs.callQueueBubble(siaPath)

	// Call complete in a go routine.
	start := time.Now()
	duration := time.Second
	go func() {
		time.Sleep(duration)
		// Call pop to prevent panic for incorrect status when complete is called
		bu := bs.managedPop()
		if bu == nil {
			t.Error("no bubble update")
			return
		}
		// calling complete should close the channel
		bs.managedCompleteBubbleUpdate(siaPath)
	}()

	// Should be blocking until after the duration
	select {
	case <-completeChan:
	case <-time.After(bubbleWaitInTestTime):
		t.Fatal("test blocked too long for bubble")
	}
	if time.Since(start) < duration {
		t.Error("complete chan closed sooner than expected")
	}

	// Complete chan should not block anymore
	select {
	case <-completeChan:
	case <-time.After(bubbleWaitInTestTime):
		t.Fatal("test blocked too long for bubble")
	}

	// If multiple calls are made to queue the same bubble update, they should all
	// block on the same channel
	start = time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeChan := bs.callQueueBubble(siaPath)
			select {
			case <-completeChan:
			case <-time.After(bubbleWaitInTestTime):
				t.Error("test blocked too long for bubble")
				return
			}
			if time.Since(start) < duration {
				t.Error("complete chan closed before time duration")
			}
		}()
	}

	// Sleep for the duration
	time.Sleep(duration)
	// Call pop to prevent panic for incorrect status when complete is called
	bu := bs.managedPop()
	if bu == nil {
		t.Fatal("no bubble update")
	}
	// calling complete should close the channel
	bs.managedCompleteBubbleUpdate(siaPath)

	// Wait for go routines to finish
	wg.Wait()

	// Queue the bubble update request
	completeChan = bs.callQueueBubble(siaPath)

	// Pop the update
	bu = bs.managedPop()
	if bu == nil {
		t.Fatal("no bubble update")
	}

	// Call queue again to update the status to pending
	//
	// The complete channel returned should be the same as the original channel
	completeChan2 := bs.callQueueBubble(siaPath)

	// Call complete
	bs.managedCompleteBubbleUpdate(siaPath)

	// Both of the original complete channels should not longer be blocking
	select {
	case <-completeChan:
	default:
		t.Error("first complete chan is still blocking")
	}
	select {
	case <-completeChan2:
	default:
		t.Error("second complete chan is still blocking")
	}

	// The complete chan in the bubble update should still be blocking
	select {
	case <-bu.complete:
		t.Error("bubble update complete chan is not blocking")
	default:
	}
}

// testBubbleScheduler_managedQueueParent probes the managedQueueParent method.
func testBubbleScheduler_managedQueueParent(t *testing.T) {
	// Initialize a bubble scheduler
	bs := newBubbleScheduler(&Renter{})

	// Calling managedQueueParent on root should be a no-op
	err := bs.managedQueueParent(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}

	// There should be nothing in the bubbleScheduler
	bs.mu.Lock()
	if len(bs.bubbleUpdates) != 0 {
		t.Error("unexpected")
	}
	if bs.fifo.Len() != 0 {
		t.Error("unexpected")
	}
	bs.mu.Unlock()

	// Call managedQueueParent on a siapath
	err = bs.managedQueueParent(modules.RandomSiaPath())
	if err != nil {
		t.Fatal(err)
	}

	// There should be a root dir update in the bubbleScheduler
	bs.mu.Lock()
	if len(bs.bubbleUpdates) != 1 {
		t.Error("unexpected")
	}
	if bs.fifo.Len() != 1 {
		t.Error("unexpected")
	}
	mapBU, ok := bs.bubbleUpdates[modules.RootSiaPath()]
	if !ok {
		t.Error("root update not found in map")
	}
	bs.mu.Unlock()
	popBU := bs.managedPop()
	if popBU == nil {
		t.Error("nil bubble update popped")
	}
	if !reflect.DeepEqual(mapBU, popBU) {
		t.Error("map and popped update don't match")
	}
}
