package returncheck_test

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/analysis/returncheck"
	"golang.org/x/tools/go/analysis/analysistest"
)

func Test(t *testing.T) {
	files := map[string]string{"a/a.go": `package a

	import (
		"fmt"
		"net/http"
	)

	type Error struct { Message string }
	type api struct {}

	func Critical(err Error) {}
	func WriteError(w http.ResponseWriter, err Error, code int) {}

	func (a *api) shouldSucceedExplicitReturn(w http.ResponseWriter) {
		WriteError(w, Error{"Bad Request"}, http.StatusBadRequest)
		return
	}

	func (a *api) shouldSucceedNoReturn(w http.ResponseWriter) {
		WriteError(w, Error{"Bad Request"}, http.StatusBadRequest)
	}

	func (a *api) shouldFailNoImmediateReturn1(w http.ResponseWriter) {
		WriteError(w, Error{"Bad Request"}, http.StatusBadRequest) // want "missing immediate return statement after call to WriteError"
		a.doSomething()
		return
	}

	func (a *api) shouldFailNoImmediateReturn2(w http.ResponseWriter) {
		WriteError(w, Error{"Bad Request"}, http.StatusBadRequest) // want "missing immediate return statement after call to WriteError"
		if true {
			return
		}
	}

	func (a *api) shouldSucceedIfStmt(w http.ResponseWriter) {
		if true {
			WriteError(w, Error{"Bad Request"}, http.StatusBadRequest)
		}
		return
	}

	func (a *api) shouldSucceedMultipleIfs(w http.ResponseWriter) {
		if true {
			WriteError(w, Error{"Bad Request"}, http.StatusBadRequest)
			return
		}
		if true {
			WriteError(w, Error{"Bad Request"}, http.StatusBadRequest)
			return
		}
	}

	func (a *api) shouldSucceedEdgeCaseMultipleIfs(w http.ResponseWriter) {
		if true {
			WriteError(w, Error{"Bad Request"}, http.StatusBadRequest)
			return
		}
		if true {
			WriteError(w, Error{"Bad Request"}, http.StatusBadRequest)
		}
	}

	func (a *api) shouldFailEdgeCaseMultipleIfs(w http.ResponseWriter) {
		if true {
			WriteError(w, Error{"Bad Request"}, http.StatusBadRequest) // want "missing immediate return statement after call to WriteError"
		}
		if true {
			WriteError(w, Error{"Bad Request"}, http.StatusBadRequest)
			return
		}
	}

	func (a *api) shouldSucceedEdgeCaseCritical(w http.ResponseWriter) {
		err := Error{"Bad Request"}
		WriteError(w, err, http.StatusBadRequest)
		Critical(err)
		return
	}

	func (a *api) shouldSucceedEdgeCasePrintln(w http.ResponseWriter) {
		WriteError(w, Error{"Bad Request"}, http.StatusBadRequest)
		fmt.Println("...")
		return
	}

	func (a *api) doSomething() {}
`}
	dir, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, returncheck.Analyzer, "a")
}
