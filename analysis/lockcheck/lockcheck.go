// Package lockcheck checks for lock misuse.
package lockcheck

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

var Analyzer = &analysis.Analyzer{
	Name:     "lockcheck",
	Doc:      "reports methods that violate locking conventions",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.FuncDecl)(nil),
	}

	inspect.Preorder(nodeFilter, func(n ast.Node) {
		fd := n.(*ast.FuncDecl)
		if fd.Recv == nil {
			return // not a method
		}

		name := fd.Name.String()
		if ast.IsExported(name) ||
			firstWordIs(name, "managed") ||
			firstWordIs(name, "threaded") ||
			firstWordIs(name, "call") {
			return // these methods manage their own locking
		}

		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if ce, ok := n.(*ast.CallExpr); ok {
				if se, ok := ce.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == "Lock" {
					if t := pass.TypesInfo.Types[se.X].Type; t != nil && strings.HasSuffix(t.String(), "Mutex") {
						pass.Reportf(n.Pos(), "unprivileged method %s locks mutex", name)
					}
				}
			}
			return true
		})
	})
	return nil, nil
}

// firstWordIs returns true if name begins with prefix, followed by an uppercase
// letter. For example, firstWordIs("startsUpper", "starts") == true, but
// firstWordIs("starts", "starts") == false.
func firstWordIs(name, prefix string) bool {
	suffix := strings.TrimPrefix(name, prefix)
	return len(suffix) > 0 && ast.IsExported(suffix)
}
