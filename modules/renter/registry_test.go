package renter

import (
	"context"
	"fmt"
	"testing"
)

// TestReadResponseSet is a unit test for the readResponseSet.
func TestReadResponseSet(t *testing.T) {
	t.Parallel()

	// Get a set and fill it up completely.
	n := 10
	c := make(chan *jobReadRegistryResponse)
	set := newReadResponseSet(c, n)
	go func() {
		for i := 0; i < n; i++ {
			c <- &jobReadRegistryResponse{staticErr: fmt.Errorf("%v", i)}
		}
	}()
	if set.responsesLeft() != n {
		t.Fatal("wrong number of responses left", set.responsesLeft(), n)
	}

	// Calling Next should work until it's empty.
	i := 0
	for set.responsesLeft() > 0 {
		resp := set.next(context.Background())
		if resp == nil {
			t.Fatal("resp shouldn't be nil")
		}
		if resp.staticErr.Error() != fmt.Sprint(i) {
			t.Fatal("wrong error", resp.staticErr, fmt.Sprint(i))
		}
		i++
	}

	// Call Next one more time and close the context while doing so.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp := set.next(ctx)
	if resp != nil {
		t.Fatal("resp should be nil")
	}

	// Collect all values.
	resps := set.collect(context.Background())
	for i, resp := range resps {
		if resp.staticErr.Error() != fmt.Sprint(i) {
			t.Fatal("wrong error", resp.staticErr, fmt.Sprint(i))
		}
	}

	// Create another set that is collected right away.
	c = make(chan *jobReadRegistryResponse)
	set = newReadResponseSet(c, n)
	go func() {
		for i := 0; i < n; i++ {
			c <- &jobReadRegistryResponse{staticErr: fmt.Errorf("%v", i)}
		}
	}()
	resps = set.collect(context.Background())
	for i, resp := range resps {
		if resp.staticErr.Error() != fmt.Sprint(i) {
			t.Fatal("wrong error", resp.staticErr, fmt.Sprint(i))
		}
	}

	// Create another set that is collected halfway and then cancelled.
	c = make(chan *jobReadRegistryResponse)
	set = newReadResponseSet(c, n/2)
	ctx, cancel = context.WithCancel(context.Background())
	go func(cancel context.CancelFunc) {
		for i := 0; i < n/2; i++ {
			c <- &jobReadRegistryResponse{staticErr: fmt.Errorf("%v", i)}
		}
		cancel()
	}(cancel)
	resps = set.collect(ctx)
	if len(resps) != n/2 {
		t.Fatal("wrong number of resps", len(resps), n/2)
	}
	for i, resp := range resps {
		if resp.staticErr.Error() != fmt.Sprint(i) {
			t.Fatal("wrong error", resp.staticErr, fmt.Sprint(i))
		}
	}

	// Collect a set without responses with a closed context.
	set = newReadResponseSet(c, n)
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	resps = set.collect(ctx)
	if len(resps) != 0 {
		t.Fatal("resps should be empty", resps)
	}
}
