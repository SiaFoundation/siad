package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
)

// TestStreamLRU checks that all of the code that forms the LRU for a
// stream
//
// This test has 100% coverage of the streambufferlru.go file.
func TestStreamLRU(t *testing.T) {
	// Create a usable stream, this creates the stream buffer that the LRU talks
	// to and gives a good opporutnity to probe the LRU.
	data := fastrand.Bytes(15999) // 1 byte short of 1000 data sections.
	dataSource := newMockDataSource(data, 16)
	sbs := newStreamBufferSet()
	stream := sbs.callNewStream(dataSource, streamDataSourceID{}, 0)

	// Extract the LRU from the stream to test it directly.
	lru := stream.lru
	// Empty the LRU so that we are working with a clean slate.
	lru.callEvictAll()
	// Set the size of the LRU to 4.
	lru.staticSize = 4

	// Check that the LRU is empty / a clean slate.
	if lru.head != nil {
		t.Fatal("lru is not empty")
	}
	if lru.tail != nil {
		t.Fatal("lru is not empty")
	}
	if len(lru.nodes) != 0 {
		t.Fatal("lru is not empty")
	}

	// Check that the stream buffer is empty.
	sb := lru.staticStreamBuffer
	if len(sb.dataSections) != 0 {
		t.Fatal("stream buffer is not empty")
	}

	// Add the first node.
	lru.callUpdate(0)
	// Check that the lru has one node.
	if lru.head == nil {
		t.Fatal("bad")
	}
	if lru.head != lru.tail {
		t.Fatal("bad")
	}
	if len(lru.nodes) != 1 {
		t.Fatal("bad")
	}
	if len(sb.dataSections) != 1 {
		t.Fatal("bad")
	}
	_, exists := sb.dataSections[0]
	if !exists {
		t.Fatal("bad")
	}

	// Add nodes 1, 2, 3, then perform an integrity check.
	lru.callUpdate(1)
	lru.callUpdate(2)
	lru.callUpdate(3)
	if len(lru.nodes) != 4 {
		t.Fatal("bad")
	}
	if len(sb.dataSections) != 4 {
		t.Fatal("bad")
	}
	if lru.head.index != 3 {
		t.Fatal("bad")
	}
	if lru.head.next.index != 2 {
		t.Fatal("bad")
	}
	if lru.head.next.next.index != 1{
		t.Fatal("bad")
	}
	if lru.tail.index != 0 {
		t.Fatal("bad")
	}
	if lru.tail.prev.index != 1 {
		t.Fatal("bad")
	}

	// Call update with 4, this should cause an eviction.
	lru.callUpdate(4)
	if len(lru.nodes) != 4 {
		t.Fatal("bad", len(lru.nodes))
	}
	if len(sb.dataSections) != 4 {
		t.Fatal("bad")
	}
	if lru.head.index != 4 {
		t.Fatal("bad")
	}
	if lru.head.next.index != 3 {
		t.Fatal("bad")
	}
	if lru.head.next.next.index != 2 {
		t.Fatal("bad")
	}
	if lru.tail.index != 1 {
		t.Fatal("bad")
	}
	if lru.tail.prev.index != 2 {
		t.Fatal("bad")
	}
	_, exists = lru.nodes[0]
	if exists {
		t.Fatal("bad")
	}
	_, exists = sb.dataSections[0]
	if exists {
		t.Fatal("bad")
	}

	// Call update with 1, this should move 1 to the head of the LRU.
	lru.callUpdate(1)
	if len(lru.nodes) != 4 {
		t.Fatal("bad", len(lru.nodes))
	}
	if len(sb.dataSections) != 4 {
		t.Fatal("bad")
	}
	if lru.head.index != 1 {
		t.Fatal("bad")
	}
	if lru.head.next.index != 4 {
		t.Fatal("bad")
	}
	if lru.head.next.next.index != 3 {
		t.Fatal("bad")
	}
	if lru.tail.index != 2 {
		t.Fatal("bad", lru.tail.index)
	}
	if lru.tail.prev.index != 3 {
		t.Fatal("bad")
	}

	// Call update with 3, this should move 3 to the head of the LRU. Unlike the
	// previous check, which updated the tail, this check updates a center node.
	lru.callUpdate(3)
	if len(lru.nodes) != 4 {
		t.Fatal("bad", len(lru.nodes))
	}
	if len(sb.dataSections) != 4 {
		t.Fatal("bad")
	}
	if lru.head.index != 3 {
		t.Fatal("bad")
	}
	if lru.head.next.index != 1 {
		t.Fatal("bad")
	}
	if lru.head.next.next.index != 4 {
		t.Fatal("bad")
	}
	if lru.head.next.next.next.index != 2 {
		t.Fatal("bad")
	}
	if lru.tail.index != 2 {
		t.Fatal("bad", lru.tail.index)
	}
	if lru.tail.prev.index != 4 {
		t.Fatal("bad")
	}
	if lru.tail.prev.prev.index != 1 {
		t.Fatal("bad")
	}
	if lru.tail.prev.prev.prev.index != 3 {
		t.Fatal("bad")
	}

	// Call update with 3 again, nothing should change.
	lru.callUpdate(3)
	if len(lru.nodes) != 4 {
		t.Fatal("bad", len(lru.nodes))
	}
	if len(sb.dataSections) != 4 {
		t.Fatal("bad")
	}
	if lru.head.index != 3 {
		t.Fatal("bad")
	}
	if lru.head.next.index != 1 {
		t.Fatal("bad")
	}
	if lru.head.next.next.index != 4 {
		t.Fatal("bad")
	}
	if lru.head.next.next.next.index != 2 {
		t.Fatal("bad")
	}
	if lru.tail.index != 2 {
		t.Fatal("bad", lru.tail.index)
	}
	if lru.tail.prev.index != 4 {
		t.Fatal("bad")
	}
	if lru.tail.prev.prev.index != 1 {
		t.Fatal("bad")
	}
	if lru.tail.prev.prev.prev.index != 3 {
		t.Fatal("bad")
	}

	// streamBuffer should have data pieces 1,2,3,4.
	_, exists = sb.dataSections[1]
	if !exists {
		t.Fatal("bad")
	}
	_, exists = sb.dataSections[2]
	if !exists {
		t.Fatal("bad")
	}
	_, exists = sb.dataSections[3]
	if !exists {
		t.Fatal("bad")
	}
	_, exists = sb.dataSections[4]
	if !exists {
		t.Fatal("bad")
	}

	// Try inserting another new node, this should evict '2'.
	lru.callUpdate(10)
	if len(lru.nodes) != 4 {
		t.Fatal("bad", len(lru.nodes))
	}
	if len(sb.dataSections) != 4 {
		t.Fatal("bad")
	}
	if lru.head.index != 10 {
		t.Fatal("bad")
	}
	if lru.head.next.index != 3 {
		t.Fatal("bad")
	}
	if lru.head.next.next.index != 1 {
		t.Fatal("bad")
	}
	if lru.head.next.next.next.index != 4 {
		t.Fatal("bad")
	}
	if lru.tail.index != 4 {
		t.Fatal("bad", lru.tail.index)
	}
	if lru.tail.prev.index != 1 {
		t.Fatal("bad")
	}
	if lru.tail.prev.prev.index != 3 {
		t.Fatal("bad")
	}
	if lru.tail.prev.prev.prev.index != 10 {
		t.Fatal("bad")
	}

	// streamBuffer should have data pieces 1,3,4,10.
	_, exists = sb.dataSections[1]
	if !exists {
		t.Fatal("bad")
	}
	_, exists = sb.dataSections[10]
	if !exists {
		t.Fatal("bad")
	}
	_, exists = sb.dataSections[3]
	if !exists {
		t.Fatal("bad")
	}
	_, exists = sb.dataSections[4]
	if !exists {
		t.Fatal("bad")
	}

	// Evict everything again.
	lru.callEvictAll()
	if len(lru.nodes) != 0 {
		t.Fatal("bad", len(lru.nodes))
	}
	if len(sb.dataSections) != 0 {
		t.Fatal("bad")
	}
	if lru.head != nil {
		t.Fatal("lru is not empty")
	}
	if lru.tail != nil {
		t.Fatal("lru is not empty")
	}
}
