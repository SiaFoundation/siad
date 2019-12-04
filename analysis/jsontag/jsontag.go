// Package jsontag defines an Analyzer that checks for violations of siad json
// struct tag conventions.
package jsontag

import (
	"go/ast"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const Doc = `check that json struct field tags conform to conventions.

siad json fields are always lowercase, and should match the Go field name.`

var Analyzer = &analysis.Analyzer{
	Name:             "jsontag",
	Doc:              Doc,
	Requires:         []*analysis.Analyzer{inspect.Analyzer},
	RunDespiteErrors: true,
	Run:              run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.StructType)(nil),
	}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		styp, ok := pass.TypesInfo.Types[n.(*ast.StructType)].Type.(*types.Struct)
		if !ok {
			return
		}
		for i := 0; i < styp.NumFields(); i++ {
			field := styp.Field(i)
			tag, ok := reflect.StructTag(styp.Tag(i)).Lookup("json")
			if !ok {
				continue
			}
			if strings.Contains(tag, ",siamismatch") {
				continue
			}
			tag = removeOpts(tag)

			if !matchesField(tag, field.Name()) {
				pass.Reportf(field.Pos(), "json struct tag %q does not match field name %q", tag, field.Name())
			} else if !isLowercase(tag) {
				pass.Reportf(field.Pos(), "json struct tag %q should be all lowercase", tag)
			}
		}
	})
	return nil, nil
}

func removeOpts(tag string) string {
	if i := strings.IndexByte(tag, ','); i != -1 {
		tag = tag[:i]
	}
	return tag
}

func matchesField(tag, field string) bool {
	tag, field = strings.ToLower(tag), strings.ToLower(field)

	// naming conventions are omitted from the comparison
	field = strings.TrimPrefix(field, "static")

	return tag == field
}

func isLowercase(tag string) bool {
	return tag == strings.ToLower(tag)
}
