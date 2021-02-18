package renter

import (
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestBubble tests the bubble code.
//
// TODO: moves bubble tests from other files into here
func TestBubble(t *testing.T) {
	// Sub tests are responsible for short vs long test determination

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
		siaPath: modules.RandomSiaPath(),
		status:  bubbleQueued,
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
			siaPath: modules.RandomSiaPath(),
			status:  bubbleQueued,
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
	t.Run("Basic", testBubbleScheduler_Basic)

	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	t.Run("Blocking", testBubbleScheduler_Blocking)
}

func testBubbleScheduler_Basic(t *testing.T) {
	// Initialize a bubble scheduler
	bs := newBubbleScheduler(&Renter{})

	// managedPop should return nil
	bu := bs.managedPop()
	if bu != nil {
		t.Error("expected nil bubble update")
	}

	// check is a helper function to check the status of the bubble update
	checkStatus := func(siaPath modules.SiaPath, status bubbleStatus, checkQueue bool) {
		// Map and queue should both contain the same update
		bu, ok := bs.bubbleUpdates[siaPath]
		if !ok {
			t.Fatal("bad")
		}
		if !bu.siaPath.Equals(siaPath) {
			t.Log("found", bu.siaPath)
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
	bs.callQueueBubble(siaPath)

	// Check the status
	checkStatus(siaPath, bubbleQueued, true)

	// Calling queue again should have no impact
	bs.callQueueBubble(siaPath)
	checkStatus(siaPath, bubbleQueued, true)

	// managedPop should return the bubble update with the status now set to
	// bubbleActive
	bu = bs.managedPop()
	checkStatus(siaPath, bubbleActive, false)
	// Queue should be empty
	if bs.fifo.Len() != 0 {
		t.Error("Queue is not empty")
	}

	// Calling queue should update the status to pending
	bs.callQueueBubble(siaPath)
	checkStatus(siaPath, bubblePending, false)
	// Queue should still be empty
	if bs.fifo.Len() != 0 {
		t.Error("Queue is not empty")
	}

	// Calling queue again should have no impact
	bs.callQueueBubble(siaPath)
	checkStatus(siaPath, bubblePending, false)
	// Queue should still be empty
	if bs.fifo.Len() != 0 {
		t.Error("Queue is not empty")
	}

	// Calling complete should add the update back to the queue
	bs.callCompleteBubbleUpdate(siaPath)
	checkStatus(siaPath, bubbleQueued, true)

	// calling managed pop and then calling complete should result in an empty map
	// and queue
	_ = bs.managedPop()
	bs.callCompleteBubbleUpdate(siaPath)
	if len(bs.bubbleUpdates) != 0 {
		t.Error("map is not empty")
	}
	if bs.fifo.Len() != 0 {
		t.Error("Queue is not empty")
	}
}

func testBubbleScheduler_Blocking(t *testing.T) {
	// multiple calls to the queue should block on the same chan and release all
	// together
	//
	// queues after pending should be a different block

}
