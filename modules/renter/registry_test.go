package renter

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// TestReadResponseSet is a unit test for the readResponseSet.
func TestReadResponseSet(t *testing.T) {
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
	go cancel()
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
	go cancel()
	resps = set.collect(ctx)
	if len(resps) != 0 {
		t.Fatal("resps should be empty", resps)
	}
}

// TestReadRegistryStats is a unit test for the readRegistryStats.
func TestReadRegistryStats(t *testing.T) {
	// Test vars.
	initialEstimate := time.Second
	startTime := time.Now()

	// Declare a helper to create response sets from responses.
	testResponseSet := func(startTime time.Time, resps ...*jobReadRegistryResponse) *readRegistryStats {
		responseChan := make(chan *jobReadRegistryResponse, len(resps))
		for _, resp := range resps {
			responseChan <- resp
		}
		rrs := newReadRegistryStats(initialEstimate)
		rrs.threadedAddResponseSet(context.Background(), startTime, newReadResponseSet(responseChan, len(resps)))
		return rrs
	}

	// Declare tests.
	tests := []struct {
		resps  []*jobReadRegistryResponse
		result time.Duration
	}{
		// No responses.
		{
			resps:  nil,
			result: initialEstimate,
		},
		// Successful response without value.
		{
			resps: []*jobReadRegistryResponse{
				{
					staticSignedRegistryValue: nil,
					staticFinishTime:          startTime.Add(time.Second * 5),
				},
			},
			result: initialEstimate,
		},
		// Response with error.
		{
			resps: []*jobReadRegistryResponse{
				{
					staticErr:        errors.New("error"),
					staticFinishTime: startTime.Add(time.Second * 5),
				},
			},
			result: initialEstimate,
		},
		// Single successful response.
		{
			resps: []*jobReadRegistryResponse{
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{},
					staticErr:                 nil,
					staticFinishTime:          startTime.Add(time.Second * 5),
				},
			},
			result: time.Millisecond * 4600,
		},
		// Mixed responses - single valid.
		{
			resps: []*jobReadRegistryResponse{
				// No response but success.
				{
					staticSignedRegistryValue: nil,
					staticFinishTime:          startTime.Add(time.Second * 1),
				},
				// Error response.
				{
					staticErr:        errors.New("error"),
					staticFinishTime: startTime.Add(time.Second * 2),
				},
				// Success.
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{
						RegistryValue: modules.RegistryValue{
							Revision: 1,
						},
					},
					staticErr:        nil,
					staticFinishTime: startTime.Add(time.Second * 5),
				},
			},
			result: time.Millisecond * 4600,
		},
		// Mixed responses - higher revision.
		{
			resps: []*jobReadRegistryResponse{
				// Success.
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{
						RegistryValue: modules.RegistryValue{
							Revision: 1,
						},
					},
					staticErr:        nil,
					staticFinishTime: startTime.Add(time.Second * 1),
				},
				// Success with higher revision but slower time.
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{
						RegistryValue: modules.RegistryValue{
							Revision: 2,
						},
					},
					staticErr:        nil,
					staticFinishTime: startTime.Add(time.Second * 5),
				},
			},
			result: time.Millisecond * 4600,
		},
		// Mixed responses - faster result.
		{
			resps: []*jobReadRegistryResponse{
				// Success.
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{
						RegistryValue: modules.RegistryValue{
							Revision: 1,
						},
					},
					staticErr:        nil,
					staticFinishTime: startTime.Add(time.Second * 1),
				},
				// Success with higher revision.
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{
						RegistryValue: modules.RegistryValue{
							Revision: 2,
						},
					},
					staticErr:        nil,
					staticFinishTime: startTime.Add(time.Second * 6),
				},
				// Success with same revision but faster.
				{
					staticSignedRegistryValue: &modules.SignedRegistryValue{
						RegistryValue: modules.RegistryValue{
							Revision: 2,
						},
					},
					staticErr:        nil,
					staticFinishTime: startTime.Add(time.Second * 5),
				},
			},
			result: time.Millisecond * 4600,
		},
	}

	// Run tests.
	for i, test := range tests {
		// Test a response set with 1 response that took 5 seconds.
		rrs := testResponseSet(startTime, test.resps...)
		if rrs.Estimate() != test.result {
			t.Fatalf("%v: results don't match %v != %v", i, rrs.Estimate(), test.result)
		}
	}
}
