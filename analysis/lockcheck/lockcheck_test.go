package lockcheck_test

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/analysis/lockcheck"
	"golang.org/x/tools/go/analysis/analysistest"
)

func Test(t *testing.T) {
	files := map[string]string{"a/a.go": `package a

import "sync"

type Foo struct {
	i  int
	mu sync.Mutex
}

func (f *Foo) bar() {
	f.mu.Lock() // want "unprivileged method bar locks mutex"
}

func (f *Foo) Bar() {
	f.mu.Lock() // OK
}

func (f *Foo) managedBar() {
	f.mu.Lock() // OK
}

func (f *Foo) threadedBar() {
	f.mu.Lock() // OK
}

func (f *Foo) callBar() {
	f.mu.Lock() // OK
}

func (f *Foo) otherprefixBar() {
	f.mu.Lock() // want "unprivileged method otherprefixBar locks mutex"
}

func (f *Foo) nonlocking() {
	f.i++ // OK
}

`}
	dir, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, lockcheck.Analyzer, "a")
}
