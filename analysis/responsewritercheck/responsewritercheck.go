// Package responsewritercheck checks that HTTP handlers do not pass the
// http.ResponseWriter to more than one function
package responsewritercheck

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/ctrlflow"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/cfg"
)

const debug = false

var Analyzer = &analysis.Analyzer{
	Name: "responsewritercheck",
	Doc:  "reports instances where HTTP handlers pass the http.Responsewriter to more than one function",
	Run:  run,
	Requires: []*analysis.Analyzer{
		inspect.Analyzer,
		ctrlflow.Analyzer,
	},
}

// responsewritercheck reports a failure when an HTTP handler passes the
// ResponseWriter to more than one function. Passing it to more than one
// function can lead to errors where the header is written multiple times,
// resulting in failure.
func run(pass *analysis.Pass) (interface{}, error) {
	if !imports(pass.Pkg, "net/http") {
		return nil, nil
	} // no import, no HTTP handlers

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	types := []ast.Node{(*ast.FuncDecl)(nil)}
	inspect.Preorder(types, func(node ast.Node) {
		fd := node.(*ast.FuncDecl)
		if !isHTTPHandler(pass, fd) {
			return
		} // not an HTTP handler

		runFunc(pass, fd)
	})

	return nil, nil
}

func runFunc(pass *analysis.Pass, fd *ast.FuncDecl) {
	// Parse ResponseWriter from the function signature
	sig, _ := pass.TypesInfo.Defs[fd.Name].Type().(*types.Signature)
	rw := sig.Params().At(0)

	// Find all expression statements that use the ResponseWriter
	stmts := make([]ast.Node, 0)
	ast.Walk(newVisitor(pass.TypesInfo, &stmts, rw), fd.Body)
	if len(stmts) == 0 {
		return
	}

	// Obtain the CFG
	cfgs := pass.ResultOf[ctrlflow.Analyzer].(*ctrlflow.CFGs)
	g := cfgs.FuncDecl(fd)
	if debug {
		fmt.Printf("Func \"%s\" CFG:\n", fd.Name.String())
		fmt.Println(g.Format(pass.Fset))
	}

	// Loop over all statements and:
	// - find the block in which it was defined
	// - find if the next nodes in that block pass the ResponseWriter
	// - find if any node on the CFG successor path passes the ResponseWriter
	for _, stmt := range stmts {
		defBlock, atIndex := defBlock(g.Blocks, stmt)
		if defBlock == nil {
			panic("could not find block where expression is defined")
		}
		nodes := defBlock.Nodes[atIndex+1:]

		for _, r := range nodes {
			if passesArgument(pass.TypesInfo, rw)(r) {
				pass.Reportf(stmt.Pos(), "http.Responsewriter passed to more than one function")
			}
		}

		if found := search(pass.TypesInfo, defBlock.Succs, rw); found != nil {
			pass.Reportf(stmt.Pos(), "http.Responsewriter passed to more than one function")
		}
	}
}

// imports returns true if the given path is being imported
func imports(pkg *types.Package, path string) bool {
	for _, imp := range pkg.Imports() {
		if imp.Path() == path {
			return true
		}
	}
	return false
}

// isHTTPHandler returns true if the given FuncDecl is an HTTP handler.
//
// We define an HTTP handler as such:
// type Handle func(http.ResponseWriter, *http.Request, Params)
// type Handle func(http.ResponseWriter, *http.Request)
func isHTTPHandler(pass *analysis.Pass, fd *ast.FuncDecl) bool {
	pc := len(fd.Type.Params.List)
	return (pc == 2 || pc == 3) &&
		isHTTPResponseWriter(pass.TypesInfo, fd.Type.Params.List[0]) &&
		isHTTPRequest(pass.TypesInfo, fd.Type.Params.List[1])
}

// isHTTPResponseWriter returns true if the field is a http.ResponseWriter
func isHTTPResponseWriter(info *types.Info, f *ast.Field) bool {
	tv, ok := info.Types[f.Type]
	if !ok {
		return false
	}
	return tv.Type.String() == "net/http.ResponseWriter"
}

// isHTTPRequest returns true if the field is a (pointer to) http.Request
func isHTTPRequest(info *types.Info, f *ast.Field) bool {
	tv, ok := info.Types[f.Type]
	if !ok {
		return false
	}
	return tv.Type.String() == "*net/http.Request"
}

// visitor keeps state when walking the AST
type visitor struct {
	conditions []func(ast.Node) bool
	output     *[]ast.Node
	depth      int // debug purposes
}

// newVisitor returns a new visitor that checks the AST for expressions passing
// the given variable as argument
func newVisitor(info *types.Info, usages *[]ast.Node, v *types.Var) visitor {
	return visitor{
		output: usages,
		conditions: []func(ast.Node) bool{
			passesArgument(info, v),
		},
	}
}

// Visit implements the ast.Visitor interface
func (v visitor) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		return v
	}
	for _, condition := range v.conditions {
		if condition(n) {
			*v.output = append(*v.output, n)
		}
	}

	if debug {
		fmt.Printf("%s%T\n", strings.Repeat("\t", v.depth), n)
	}

	v.depth++
	return v
}

// search will walk the CFG path of successor blocks looking for nodes that pass
// the given variable as argument
func search(info *types.Info, blocks []*cfg.Block, v *types.Var) ast.Node {
	for _, b := range blocks {
		for _, n := range b.Nodes {
			if passesArgument(info, v)(n) {
				return n
			}
		}
		if rec := search(info, b.Succs, v); rec != nil {
			return rec
		}
	}

	return nil
}

// debBlock returns the block (and node index) where the given node was defined
func defBlock(blocks []*cfg.Block, node ast.Node) (*cfg.Block, int) {
	for _, b := range blocks {
		for i, n := range b.Nodes {
			if n == node {
				return b, i
			}
		}
	}
	return nil, -1
}

// passesArgument returns a function that returns true if the given node is a
// call expression that has the given var as one of its arguments
func passesArgument(info *types.Info, v *types.Var) func(ast.Node) bool {
	return func(n ast.Node) bool {
		if n == nil {
			return false
		}
		switch d := n.(type) {
		case *ast.ExprStmt:
			if ce, ok := d.X.(*ast.CallExpr); ok {
				for _, arg := range ce.Args {
					if ident, ok := arg.(*ast.Ident); ok {
						if info.Uses[ident] == v {
							return true
						}
					}
				}
			}
		}
		return false
	}
}
