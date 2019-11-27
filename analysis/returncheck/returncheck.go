// Package returncheck checks for calls to WriteError which are not
// immediately followed by a return statement
package returncheck

import (
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/ctrlflow"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/cfg"
)

var Analyzer = &analysis.Analyzer{
	Name: "returncheck",
	Doc:  "reports instances where WriteError is not immediately followed by a return statement",
	Run:  run,
	Requires: []*analysis.Analyzer{
		inspect.Analyzer,
		ctrlflow.Analyzer,
	},
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	cfgs := pass.ResultOf[ctrlflow.Analyzer].(*ctrlflow.CFGs)

	nodeFilter := []ast.Node{
		(*ast.FuncDecl)(nil),
	}

	inspect.Preorder(nodeFilter, func(node ast.Node) {
		fd := node.(*ast.FuncDecl)
		if fd.Recv == nil {
			return
		} // not a method

		g := cfgs.FuncDecl(fd)
		for _, b := range g.Blocks {
			for curr, n := range b.Nodes {
				if pos := posOfCallTo(n, "WriteError"); pos.IsValid() {
					if !isReturnStmt(nextNode(b, curr)) {
						pass.Reportf(pos, "missing immediate return statement after call to WriteError")
					}
				}
			}
		}
	})

	return nil, nil
}

// nextNode returns the node immediately following the node in the given block
// at curr index
func nextNode(b *cfg.Block, curr int) ast.Node {
	switch len(b.Succs) {
	case 0:
		for next := curr + 1; next < len(b.Nodes); next++ {
			if isException(b.Nodes[next]) {
				continue
			}
			return b.Nodes[next]
		}
	case 1:
		for next := 0; next < len(b.Succs[0].Nodes); next++ {
			if isException(b.Succs[0].Nodes[next]) {
				continue
			}
			return b.Succs[0].Nodes[next]
		}
	}
	return nil
}

// isReturnStmt returns true if the given node is a return statement
func isReturnStmt(n ast.Node) bool { _, ok := n.(*ast.ReturnStmt); return ok }

// isException will return true if the node represents a call to a set of
// functions we wish to exclude and allow is immediate next node. For example,
// we want to allow calling build.Critical after WriteError as long as it's
// immediately followed by a return statement.
func isException(n ast.Node) bool {
	return posOfCallTo(n, "Critical").IsValid() ||
		posOfCallTo(n, "Println").IsValid() ||
		posOfCallTo(n, "Printf").IsValid()
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
