package a

import "sync"

type Foo struct {
	i  int
	mu sync.Mutex
}

func (f *Foo) bar() {
	f.mu.Lock() // want "unprivileged method bar locks mutex"
	f.mu.Unlock()
}

func (f *Foo) Bar() {
	f.mu.Lock() // OK
	f.mu.Unlock()
}

func (f *Foo) managedBar() {
	f.mu.Lock() // OK
	f.mu.Unlock()
}

func (f *Foo) threadedBar() {
	f.mu.Lock() // OK
	f.mu.Unlock()
}

func (f *Foo) callBar() {
	f.mu.Lock() // OK
	f.mu.Unlock()
}

func (f *Foo) otherprefixBar() {
	f.mu.Lock() // want "unprivileged method otherprefixBar locks mutex"
	f.mu.Unlock()
}

func (f *Foo) nonlocking() {
	f.i++ // OK
}

func (f *Foo) callsUnprivileged() {
	f.bar() // OK
}

func (f *Foo) callsPrivileged() {
	f.managedBar() // want "unprivileged method callsPrivileged calls privileged method managedBar"
}

func (f *Foo) ExportedNonLocking() {
	f.i++ // want "privileged method ExportedNonLocking accesses i without holding mutex"
}

func (f *Foo) ExportedLocking() {
	f.mu.Lock()
	f.i++ // OK
	f.mu.Unlock()
}

func (f *Foo) ExportedDeferLocking() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.i++ // OK
}

func (f *Foo) ExportedUnlocking() {
	f.mu.Lock()
	f.mu.Unlock()
	f.i++ // want "privileged method ExportedUnlocking accesses i without holding mutex"
}

func (f *Foo) ExportedLoopLocking() {
	f.mu.Lock()
	for i := 0; i < 10; i++ {
		f.mu.Unlock()
		f.mu.Lock()
	}
	f.i++ // OK
}

func (f *Foo) OnePathLocks() {
	if true {
		f.mu.Lock()
	}
	f.i++ // want "privileged method OnePathLocks accesses i without holding mutex"
}

func (f *Foo) AllPathsLock() {
	if true {
		f.mu.Lock()
	} else {
		f.mu.Lock()
	}
	f.i++ // OK
	f.mu.Unlock()
}

func (f *Foo) OnePathDoesNotLock() {
	if 1 < 2 {
		if 2 < 3 {
			if 4 < 3 {
				f.mu.Lock()
			}
		}
		if 5 < 6 {
			f.mu.Lock()
		} else {
			f.mu.Unlock()
		}
	}
	if 2 < 1 {
		f.i++ // want "privileged method OnePathDoesNotLock accesses i without holding mutex"
	}
}

func (f *Foo) CallsPrivilegedWithLock() {
	f.mu.Lock()
	f.Bar() // want "privileged method CallsPrivilegedWithLock calls privileged method Bar while holding mutex"
}

func (f *Foo) CallsUnprivilegedWithoutLock() {
	f.bar() // want "privileged method CallsUnprivilegedWithoutLock calls unprivileged method bar without holding mutex"
}

func (f *Foo) staticBar() {}

func (f *Foo) CallsStaticWithoutLock() {
	f.staticBar() // OK
}

func (f *Foo) CallLiteral() {
	func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.i++ // OK
	}()

	f.mu.Lock()
	defer f.mu.Unlock()
	func() {
		f.i++ // OK
	}()
}

func (f *Foo) CallAssignedLiteral() {
	fn := func() {
		f.mu.Lock()
		if true {
			f.i++ // OK
		}
		f.mu.Unlock()
	}
	fn()
}

type FooNoMutex struct {
	i int
}

func (f *FooNoMutex) Bar() {
	f.i++ // OK
}

func (f *FooNoMutex) baz() {
	f.Bar() // OK
}

type FooUnrelatedExportedMethod struct {
	other Foo
	mu    sync.Mutex
}

func (f *FooUnrelatedExportedMethod) bar() {
	f.other.Bar() // OK
}
