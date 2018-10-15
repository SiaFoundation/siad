package main

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
)

// TestNewServer verifies that NewServer creates a Sia API server correctly.
func TestNewServer(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	var wg sync.WaitGroup
	config := Config{}
	config.Siad.APIaddr = "localhost:0"
	config.Siad.Modules = "cg"
	config.Siad.SiaDir = build.TempDir(t.Name())
	defer os.RemoveAll(config.Siad.SiaDir)
	srv, err := NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := srv.Serve()
		if err != nil {
			t.Fatal(err)
		}
	}()
	// verify that startup routes can be called correctly
	c := client.New(srv.listener.Addr().String())
	_, err = c.DaemonVersionGet()
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ConsensusGet()
	if err == nil || !strings.Contains(err.Error(), "siad is not ready") {
		t.Fatal("expected consensus call on unloaded server to fail with siad not ready")
	}
	// create a goroutine that continuously makes API requests to test that
	// loading modules doesn't cause a race
	wg.Add(1)
	stopchan := make(chan struct{})
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopchan:
				return
			default:
			}
			time.Sleep(time.Millisecond)
			c.ConsensusGet()
		}
	}()
	// load the modules, verify routes succeed
	err = srv.loadModules()
	if err != nil {
		t.Fatal(err)
	}
	close(stopchan)
	_, err = c.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	wg.Wait()
}
