package sync

import (
	"context"
	"errors"
	"sync"
)

// ErrStopped is returned by ThreadGroup methods if Stop has already been
// called.
var ErrStopped = errors.New("ThreadGroup already stopped")

// A ThreadGroup is a one-time-use object to manage the life cycle of a group
// of threads. It is a sync.WaitGroup that provides functions for coordinating
// actions and shutting down threads. After Stop() is called, the thread group
// is no longer useful.
//
// It is safe to call Add(), Done(), and Stop() concurrently, however it is not
// safe to nest calls to Add(). A simple example of a nested call to add would
// be:
//
//	tg.Add()
//	tg.Add()
//	tg.Done()
//	tg.Done()
type ThreadGroup struct {
	onStopFns    []func()
	afterStopFns []func()
	mu           sync.Mutex // Protects the 'onStopFns' and 'afterStopFns' variable

	once          sync.Once
	stopCtx       context.Context
	stopCtxCancel context.CancelFunc
	bmu           sync.Mutex // Ensures blocking between calls to 'Add', 'Flush', and 'Stop'
	wg            sync.WaitGroup
}

// init creates the stop channel for the thread group.
func (tg *ThreadGroup) init() {
	tg.stopCtx, tg.stopCtxCancel = context.WithCancel(context.Background())
}

// isStopped will return true if Stop() has been called on the thread group.
func (tg *ThreadGroup) isStopped() bool {
	tg.once.Do(tg.init)
	select {
	case <-tg.stopCtx.Done():
		return true
	default:
		return false
	}
}

// Add increments the thread group counter.
func (tg *ThreadGroup) Add() error {
	tg.bmu.Lock()
	defer tg.bmu.Unlock()

	if tg.isStopped() {
		return ErrStopped
	}
	tg.wg.Add(1)
	return nil
}

// AfterStop ensures that a function will be called after Stop() has been
// called and after all running routines have called Done(). The functions will
// be called in reverse order to how they were added, similar to defer. If
// Stop() has already been called, the input function will be called
// immediately.
//
// The primary use of AfterStop is to allow code that opens and closes
// resources to be positioned next to each other. The purpose is similar to
// `defer`, except for resources that outlive the function which creates them.
func (tg *ThreadGroup) AfterStop(fn func()) {
	tg.mu.Lock()
	defer tg.mu.Unlock()

	if tg.isStopped() {
		fn()
		return
	}
	tg.afterStopFns = append(tg.afterStopFns, fn)
}

// OnStop ensures that a function will be called after Stop() has been called,
// and before blocking until all running routines have called Done(). It is
// safe to use OnStop to coordinate the closing of long-running threads. The
// OnStop functions will be called in the reverse order in which they were
// added, similar to defer. If Stop() has already been called, the input
// function will be called immediately.
func (tg *ThreadGroup) OnStop(fn func()) {
	tg.mu.Lock()
	defer tg.mu.Unlock()

	if tg.isStopped() {
		fn()
		return
	}
	tg.onStopFns = append(tg.onStopFns, fn)
}

// Done decrements the thread group counter.
func (tg *ThreadGroup) Done() {
	tg.wg.Done()
}

// Flush will block all calls to 'tg.Add' until all current routines have
// called 'tg.Done'. This in effect 'flushes' the module, letting it complete
// any tasks that are open before taking on new ones.
func (tg *ThreadGroup) Flush() error {
	tg.bmu.Lock()
	defer tg.bmu.Unlock()

	if tg.isStopped() {
		return ErrStopped
	}
	tg.wg.Wait()
	return nil
}

// Stop will close the stop channel of the thread group, then call all 'OnStop'
// functions in reverse order, then will wait until the thread group counter
// reaches zero, then will call all of the 'AfterStop' functions in reverse
// order. After Stop is called, most actions will return ErrStopped.
func (tg *ThreadGroup) Stop() error {
	// Establish that Stop has been called.
	tg.bmu.Lock()
	if tg.isStopped() {
		tg.bmu.Unlock()
		return ErrStopped
	}
	// Close the context while holding the lock. That way the next thread
	// calling "Stop" will get an "ErrStopped".
	tg.stopCtxCancel()
	tg.bmu.Unlock()

	// Flush any function that made it past isStopped and might be trying to do
	// something under the mu lock. Any calls to OnStop or AfterStop after this
	// will fail, because isStopped will cut them short.
	tg.mu.Lock()
	tg.mu.Unlock()

	for i := len(tg.onStopFns) - 1; i >= 0; i-- {
		tg.onStopFns[i]()
	}
	tg.onStopFns = nil

	// Wait for all running processes to signal completion.
	tg.wg.Wait()

	// After waiting for all resources to release the thread group, iterate
	// through the stop functions and call them in reverse oreder.
	for i := len(tg.afterStopFns) - 1; i >= 0; i-- {
		tg.afterStopFns[i]()
	}
	tg.afterStopFns = nil
	return nil
}

// StopChan provides read-only access to the ThreadGroup's stopChan. Callers
// should select on StopChan in order to interrupt long-running reads (such as
// time.After).
func (tg *ThreadGroup) StopChan() <-chan struct{} {
	tg.once.Do(tg.init)
	return tg.stopCtx.Done()
}

// StopCtx returns the threadgroups underlying context.
func (tg *ThreadGroup) StopCtx() context.Context {
	tg.once.Do(tg.init)
	return tg.stopCtx
}
