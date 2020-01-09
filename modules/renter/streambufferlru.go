package renter

import (
	"sync"
)

// leastRecentlyUsedCacheNode is a node in the leastRecentlyUsedCache. It has an
// index which represents which data is being stored by the node in the stream
// buffer, and it has a pointer to the previous and next elements in the LRU.
type leastRecentlyUsedCacheNode struct {
	index uint64
	prev  *leastRecentlyUsedCacheNode
	next  *leastRecentlyUsedCacheNode
}

// leastRecentlyUsedCache is an LRU cache for a stream. The implementation is a
// doubly linked list of nodes that have been used, where the list is sorted by
// how recently each node was used. There is a map that points to every element
// of the list.
type leastRecentlyUsedCache struct {
	head  *leastRecentlyUsedCacheNode
	tail  *leastRecentlyUsedCacheNode
	nodes map[uint64]*leastRecentlyUsedCacheNode

	// A single mutex to cover the entire LRU cache, including all of the nodes.
	mu                 sync.Mutex
	staticSize         uint64
	staticStreamBuffer *streamBuffer
}

// newLeastRecentlyUsedCache initializes an lru.
func newLeastRecentlyUsedCache(size uint64, sb *streamBuffer) *leastRecentlyUsedCache {
	return &leastRecentlyUsedCache{
		nodes: make(map[uint64]*leastRecentlyUsedCacheNode),

		staticSize:         size,
		staticStreamBuffer: sb,
	}
}

// callEvictAll will remove all nodes from the lru and release their
// corresponding data sections on the stream buffer.
func (lru *leastRecentlyUsedCache) callEvictAll() {
	// Record the head of the lru and then wipe everything clean.
	lru.mu.Lock()
	head := lru.head
	lru.head = nil
	lru.tail = nil
	lru.nodes = make(map[uint64]*leastRecentlyUsedCacheNode)
	lru.mu.Unlock()

	// Loop through the linked list and release every index.
	for head != nil {
		lru.staticStreamBuffer.callRemoveDataSection(head.index)
		head = head.next
	}
}

// callUpdate is called when a node in the LRU is accessed. This will cause that
// node to be placed at the most recent point of the LRU. If the node is not
// currently in the LRU and the LRU is full, the least recently used node of the
// LRU will be evicted.
func (lru *leastRecentlyUsedCache) callUpdate(index uint64) {
	lru.mu.Lock()
	// Check if the node is already in the LRU. If so, move that node to the
	// front of the list.
	node, exists := lru.nodes[index]
	if exists {
		lru.moveToFront(node)
		lru.mu.Unlock()
		return
	}
	// Create a new node and insert that node at the head. 'insertHead' will
	// take care of setting all the pointer values.
	node = &leastRecentlyUsedCacheNode{
		index: index,
	}
	lru.insertHead(node)
	lru.mu.Unlock()
	lru.staticStreamBuffer.callFetchDataSection(index)

	// Eviction needs to straddle the consistency domain of the lru and the
	// consistency domain of the stream buffer, so it has to be a managed call.
	lru.managedEvict()
}

// managedEvict will evict the least recently used node of the cache.
func (lru *leastRecentlyUsedCache) managedEvict() {
	// Check whether any eviction needs to happen. Nothing to do if not.
	lru.mu.Lock()
	if uint64(len(lru.nodes)) <= lru.staticSize {
		lru.mu.Unlock()
		return
	}
	// Grab the current tail to evict it.
	node := lru.tail
	// Set the previous node's next to nil.
	node.prev.next = nil
	// Set the previous node to the new tail.
	lru.tail = node.prev
	delete(lru.nodes, node.index)
	lru.mu.Unlock()

	// Once the lru has been updated, get the node's data removed from the
	// stream buffer.
	lru.staticStreamBuffer.callRemoveDataSection(node.index)
}

// insertHead will place a new node at the head of the cache.
func (lru *leastRecentlyUsedCache) insertHead(node *leastRecentlyUsedCacheNode) {
	// Put the new node into the lru map.
	lru.nodes[node.index] = node
	// If there is no current head, make this node the head and the tail.
	if lru.head == nil {
		lru.head = node
		lru.tail = node
		return
	}
	// Set prev for the current head.
	lru.head.prev = node
	// Set the next of the node to the current head.
	node.next = lru.head
	// Set the lru head to the new node.
	lru.head = node
}

// moveToFront accepts a node that is already in the LRU and then moves that
// node to the front of the LRU.
func (lru *leastRecentlyUsedCache) moveToFront(node *leastRecentlyUsedCacheNode) {
	// If the node is already at the front, there is nothing to do.
	if node.prev == nil {
		return
	}

	// Point the previous node to the next node. We know a previous node exists
	// because this node is not the head node.
	node.prev.next = node.next
	// Point the next node to the previous node. Need to check that there is a
	// next node before doing it.
	if node.next != nil {
		node.next.prev = node.prev
	}
	// If this node is the tail, point the tail to this node.
	if lru.tail == node {
		lru.tail = node.prev
	}

	// Point the node's next value to the current head.
	node.next = lru.head
	// Point the node's previous value to nothing.
	node.prev = nil
	// Point the current head's previous value to the node.
	lru.head.prev = node
	// Point the lru head to the node.
	lru.head = node
	return
}
