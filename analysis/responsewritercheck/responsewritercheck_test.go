package responsewritercheck

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func Test(t *testing.T) {
	files := map[string]string{"a/a.go": `package a

	import "net/http"

	type api struct {}
	func WriteSuccess(w http.ResponseWriter) {}
	func WriteError(w http.ResponseWriter) {}

	func (a *api) httpHandlerBasic(w http.ResponseWriter, req *http.Request) {
		WriteSuccess(w) // OK
	}

	func (a *api) httpHandlerBasicFail(w http.ResponseWriter, req *http.Request) {
		WriteSuccess(w) // want "http.Responsewriter passed to more than one function"
		WriteError(w) // OK
	}

	func (a *api) httpHandlerIf(w http.ResponseWriter, req *http.Request) {
		if true {
			WriteError(w) // want "http.Responsewriter passed to more than one function"
		}
		WriteSuccess(w) // OK
	}

	func (a *api) httpHandlerIfRet(w http.ResponseWriter, req *http.Request) {
		if true {
			WriteError(w) // OK
			return
		}
		WriteSuccess(w) // OK
	}

	func (a *api) httpHandlerIfElse(w http.ResponseWriter, req *http.Request) {
		if true {
			WriteError(w) // OK
		} else {
			WriteSuccess(w) // OK
		}
	}

	func (a *api) httpHandlerIfElseFail(w http.ResponseWriter, req *http.Request) {
		if true {
			WriteError(w) // OK
			return
		} else {
			WriteSuccess(w) // want "http.Responsewriter passed to more than one function"
		}
		WriteSuccess(w) // OK
	}

	func (a *api) httpHandlerMultipleIfs(w http.ResponseWriter, req *http.Request) {
		if true {
			WriteError(w) // OK
			return
		}
		if true {
			WriteError(w) // OK
			return
		}
		WriteSuccess(w) // OK
	}
`}
	dir, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, Analyzer, "a")
}
