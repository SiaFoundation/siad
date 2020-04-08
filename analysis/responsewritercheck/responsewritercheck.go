// Package responsewritercheck checks that HTTP handlers do not pass the
// http.ResponseWriter to more than one function
package responsewritercheck

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/ctrlflow"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/cfg"
)

// Analyzer defines the responsewritercheck analysis tool, allowing it to be
// used with the analysis framework.
var Analyzer = &analysis.Analyzer{
	Name: "responsewritercheck",
	Doc:  "reports instances where HTTP handlers pass the http.Responsewriter to more than one function",
	Run:  run,
	Requires: []*analysis.Analyzer{
		inspect.Analyzer,
		ctrlflow.Analyzer,
	},
}

// VisitorFunc is an adapter for the ast.Visitor interface. It allows passing in
// anonymous functions that adhere to the Visitor interface.
type VisitorFunc func(n ast.Node) ast.Visitor

// Visit implements ast.Visitor by calling f.
func (f VisitorFunc) Visit(n ast.Node) ast.Visitor {
	return f(n)
}

// responsewritercheck reports a failure when an HTTP handler passes the
// ResponseWriter to more than one function. Passing it to more than one
// function can lead to errors where the header is written multiple times,
// resulting in failure.
func run(pass *analysis.Pass) (interface{}, error) {
	// fast path, skip if net/http is not imported
	if !imports(pass.Pkg, "net/http") {
		return nil, nil
	}

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	filter := []ast.Node{(*ast.FuncDecl)(nil)}
	inspect.Preorder(filter, func(node ast.Node) {
		fd := node.(*ast.FuncDecl)
		rw, exists := paramHTTPResponseWriter(pass.TypesInfo, fd)
		if !exists {
			return
		}
		runFunc(pass, fd, rw)
	})

	return nil, nil
}

// runFunc checks the usage of responseWriter within fd.
func runFunc(pass *analysis.Pass, fd *ast.FuncDecl, responseWriter *types.Var) {
	// Find all expression staments that use the http.ResponseWriter
	var usages []ast.Node
	var v ast.Visitor
	v = VisitorFunc(func(node ast.Node) ast.Visitor {
		if passesArgument(pass.TypesInfo, node, responseWriter) {
			usages = append(usages, node)
		}
		return v
	})
	ast.Walk(v, fd.Body)

	// Obtain the CFG
	cfgs := pass.ResultOf[ctrlflow.Analyzer].(*ctrlflow.CFGs)
	g := cfgs.FuncDecl(fd)

	// Loop over all statements and:
	// - find the block in which it was defined
	// - find if the next nodes in that block pass the ResponseWriter
	// - find if any node on the CFG successor path passes the ResponseWriter
	for _, stmt := range usages {
		defBlock, atIndex := defBlock(g.Blocks, stmt)
		if defBlock == nil {
			panic("could not find block where expression is defined")
		}
		nodes := defBlock.Nodes[atIndex+1:]

		for _, n := range nodes {
			if passesArgument(pass.TypesInfo, n, responseWriter) {
				pass.Reportf(stmt.Pos(), "http.Responsewriter passed to more than one function")
			}
		}

		visited := make(map[*cfg.Block]bool)
		if found := search(pass.TypesInfo, visited, defBlock.Succs, responseWriter); found != nil {
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

// paramHTTPResponseWriter returns the http.ResponseWriter param
func paramHTTPResponseWriter(info *types.Info, fd *ast.FuncDecl) (*types.Var, bool) {
	for i, fld := range fd.Type.Params.List {
		if isHTTPResponseWriter(info, fld) {
			sig, _ := info.Defs[fd.Name].Type().(*types.Signature)
			return sig.Params().At(i), true
		}
	}
	return nil, false
}

// isHTTPResponseWriter returns true if the field is a http.ResponseWriter
func isHTTPResponseWriter(info *types.Info, f *ast.Field) bool {
	tv, ok := info.Types[f.Type]
	if !ok {
		return false
	}
	return tv.Type.String() == "net/http.ResponseWriter"
}

// search will walk the CFG path of successor blocks looking for nodes that pass
// the given variable as argument
func search(info *types.Info, visited map[*cfg.Block]bool, blocks []*cfg.Block, v *types.Var) ast.Node {
	for _, b := range blocks {
		if visited[b] {
			continue
		}
		visited[b] = true

		for _, n := range b.Nodes {
			if passesArgument(info, n, v) {
				return n
			}
		}
		if rec := search(info, visited, b.Succs, v); rec != nil {
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
func passesArgument(info *types.Info, n ast.Node, v *types.Var) bool {
	if n == nil {
		return false
	}

	var es *ast.ExprStmt
	switch d := n.(type) {
	case *ast.ExprStmt:
		es = d
	default:
		return false
	}

	ce, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}

	for _, arg := range ce.Args {
		ident, ok := arg.(*ast.Ident)
		if !ok {
			continue
		}
		if info.Uses[ident] == v {
			return true
		}
	}

	return false
}
