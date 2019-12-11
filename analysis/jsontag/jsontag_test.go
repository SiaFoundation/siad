package jsontag_test

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/analysis/jsontag"
	"golang.org/x/tools/go/analysis/analysistest"
)

func Test(t *testing.T) {
	files := map[string]string{"a/a.go": `package a

type Foo struct {
	a int // OK

	b int "json:\"b\"" // OK
	C int "json:\"c\"" // OK
	D int "json:\"d,omitempty\"" // OK
	e int "json:\"E\"" // want "json struct tag \"E\" should be all lowercase"
	f int "json:\"g\"" // want "json struct tag \"g\" does not match field name \"f\""

	g int "json:\"h,siamismatch\"" // OK

	StaticH     int "json:\"h,omitempty\"" // OK
	DeprecatedI int "json:\"iDeprecated,siamismatch\"" // OK

	j int "json:\"j,string,foo,bar\"" // OK
}

`}
	dir, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, jsontag.Analyzer, "a")
}
