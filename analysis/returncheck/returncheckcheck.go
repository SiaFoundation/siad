// Package returncheck checks for calls to WriteError which are not
// immediately followed by a return statement
package returncheck

import (
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

var Analyzer = &analysis.Analyzer{
	Name:     "returncheck",
	Doc:      "reports instances where WriteError is not immediately followed by a return statement",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.FuncDecl)(nil),
	}

	var pos token.Pos
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		fd := n.(*ast.FuncDecl)
		if fd.Recv == nil {
			return
		} // not a method

		var expectingReturn bool
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if n == nil {
				return false
			}

			if posOfCallTo(n, "Critical").IsValid() ||
				posOfCallTo(n, "Println").IsValid() ||
				posOfCallTo(n, "Printf").IsValid() {
				return false
			} // calls to the above functions are accepted after a WriteError

			if expectingReturn {
				if _, ok := n.(*ast.ReturnStmt); !ok {
					pass.Reportf(pos, "missing immediate return statement after call to WriteError")
				}
				expectingReturn = false
				return false
			}

			if pos = posOfCallTo(n, "WriteError"); pos.IsValid() {
				expectingReturn = true
				return false
			}
			return true
		})
	})
	return nil, nil
}

// posOfCallTo will look for a function call with given name and return its
// position, if not found we return the NoPos zero value
func posOfCallTo(n ast.Node, name string) token.Pos {
	if es, ok := n.(*ast.ExprStmt); ok {
		if ce, ok := es.X.(*ast.CallExpr); ok {
			if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == name {
				return id.Pos()
			}
			if se, ok := ce.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == name {
				return se.Pos()
			}
		}
	}

	if ce, ok := n.(*ast.CallExpr); ok {
		if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == name {
			return id.Pos()
		}
		if se, ok := ce.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == name {
			return se.Pos()
		}
	}

	return token.NoPos
}
