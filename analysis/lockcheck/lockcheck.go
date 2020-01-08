// Package lockcheck checks for lock misuse.
package lockcheck

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/ctrlflow"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/cfg"
)

var Analyzer = &analysis.Analyzer{
	Name: "lockcheck",
	Doc:  "reports methods that violate locking conventions",
	Run:  run,
	Requires: []*analysis.Analyzer{
		inspect.Analyzer,
		ctrlflow.Analyzer,
	},
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
		// if there's no receiver list (e.g. func (Foo) bar()), skip this method, since it obviously
		// can't access a mutex or any other methods
		if len(fd.Recv.List) == 0 || len(fd.Recv.List[0].Names) == 0 {
			return
		}
		recv := pass.TypesInfo.Defs[fd.Recv.List[0].Names[0]]
		mu, ok := containsMutex(pass, recv)
		if !ok {
			return
		}
		checkLockSafety(pass, fd, recv, mu)
	})
	return nil, nil
}

func isMutexType(t types.Type) bool {
	return strings.HasSuffix(t.String(), "Mutex")
}

func containsMutex(pass *analysis.Pass, recv types.Object) (types.Object, bool) {
	if p, ok := recv.Type().Underlying().(*types.Pointer); ok {
		if s, ok := p.Elem().Underlying().(*types.Struct); ok {
			for i := 0; i < s.NumFields(); i++ {
				if f := s.Field(i); isMutexType(f.Type()) && s.Field(i).Name() == "mu" {
					return s.Field(i), true
				}
			}
		}
	}
	return nil, false
}

func checkLockSafety(pass *analysis.Pass, fd *ast.FuncDecl, recv types.Object, recvMu types.Object) {
	name := fd.Name.String()
	recvIsPrivileged := managesOwnLocking(name)

	cfgs := pass.ResultOf[ctrlflow.Analyzer].(*ctrlflow.CFGs)

	isLock := func(block ast.Node) bool {
		var found bool
		ast.Inspect(block, func(n ast.Node) bool {
			if found {
				return false
			}
			// don't descend into DeferStmt or FuncLit
			if _, ok := n.(*ast.DeferStmt); ok {
				return false
			} else if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if ce, ok := n.(*ast.CallExpr); ok {
				if fnse, ok := ce.Fun.(*ast.SelectorExpr); ok {
					if strings.HasSuffix(fnse.Sel.Name, "Lock") {
						if se, ok := fnse.X.(*ast.SelectorExpr); ok {
							if sel, ok := pass.TypesInfo.Selections[se]; ok && sel.Obj() == recvMu {
								found = true
							}
						}
					}
				}
			}
			return true
		})
		return found
	}
	isUnlock := func(block ast.Node) bool {
		var found bool
		ast.Inspect(block, func(n ast.Node) bool {
			if found {
				return false
			}
			// don't descend into DeferStmt or FuncLit
			if _, ok := n.(*ast.DeferStmt); ok {
				return false
			} else if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if ce, ok := n.(*ast.CallExpr); ok {
				if fnse, ok := ce.Fun.(*ast.SelectorExpr); ok {
					if strings.HasSuffix(fnse.Sel.Name, "Unlock") {
						if se, ok := fnse.X.(*ast.SelectorExpr); ok {
							if sel, ok := pass.TypesInfo.Selections[se]; ok && sel.Obj() == recvMu {
								found = true
							}
						}
					}
				}
			}
			return true
		})
		return found
	}
	isFieldAccess := func(block ast.Node) (field *ast.Ident, ok bool) {
		ast.Inspect(block, func(n ast.Node) bool {
			if field != nil {
				return false // already found
			}
			if _, ok := n.(*ast.FuncLit); ok {
				return false // don't descend into FuncLits
			}
			se, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := se.X.(*ast.Ident); ok && pass.TypesInfo.Uses[x] == recv {
				// other mutexes are considered safe to access
				if isMutexType(pass.TypesInfo.TypeOf(se.Sel)) {
					return true
				}
				field = se.Sel
				return false // no need to search further
			}
			return true
		})
		return field, field != nil
	}
	isRecvMethodCall := func(block ast.Node) (method string, ok bool) {
		ast.Inspect(block, func(n ast.Node) bool {
			if method != "" {
				return false // already found
			}
			if _, ok := n.(*ast.FuncLit); ok {
				return false // don't descend into FuncLits
			}
			if ce, ok := n.(*ast.CallExpr); ok {
				if se, ok := ce.Fun.(*ast.SelectorExpr); ok {
					if x, ok := se.X.(*ast.Ident); ok && pass.TypesInfo.Uses[x] == recv {
						method = se.Sel.Name
						return false // no need to search further
					}
				}
			}
			return true
		})
		return method, method != ""
	}

	isFuncLitCall := func(block ast.Node) (litBlock *cfg.Block, ok bool) {
		ast.Inspect(block, func(n ast.Node) bool {
			if litBlock != nil {
				return false // already found
			}
			if ce, ok := n.(*ast.CallExpr); ok {
				switch fn := ce.Fun.(type) {
				case *ast.FuncLit:
					litBlock = cfgs.FuncLit(fn).Blocks[0]
				case *ast.Ident:
					// TODO: make this more generic (currently only handles single assignment)
					if fn.Obj != nil {
						if as, ok := fn.Obj.Decl.(*ast.AssignStmt); ok {
							if lit, ok := as.Rhs[0].(*ast.FuncLit); ok {
								litBlock = cfgs.FuncLit(lit).Blocks[0]
							}
						}
					}
				}
			}
			return true
		})
		return litBlock, litBlock != nil
	}

	// Recursively visit each path through the function, noting the possible
	// lock states at each block.
	type edge struct {
		to, from int32
		locked   bool
	}
	visited := make(map[edge]struct{})
	var checkPath func(*cfg.Block, bool)
	checkPath = func(b *cfg.Block, lockHeld bool) {
		for _, n := range b.Nodes {
			if litBlock, ok := isFuncLitCall(n); ok {
				checkPath(litBlock, lockHeld)
				continue
			}
			if isLock(n) {
				if !recvIsPrivileged {
					pass.Reportf(n.Pos(), "unprivileged method %s locks mutex", name)
				}
				lockHeld = true
			} else if isUnlock(n) {
				lockHeld = false
			} else if method, ok := isRecvMethodCall(n); ok {
				if recvIsPrivileged {
					if managesOwnLocking(method) && !firstWordIs(method, "threaded") && lockHeld {
						pass.Reportf(n.Pos(), "privileged method %s calls privileged method %s while holding mutex", name, method)
					} else if !managesOwnLocking(method) && !lockHeld {
						pass.Reportf(n.Pos(), "privileged method %s calls unprivileged method %s without holding mutex", name, method)
					}
				} else if managesOwnLocking(method) {
					pass.Reportf(n.Pos(), "unprivileged method %s calls privileged method %s", name, method)
				}
			} else if field, ok := isFieldAccess(n); ok && !firstWordIs(field.Name, "static") && !lockHeld {
				// NOTE: a method call is also considered a field access, so
				// it's important that we only examine field accesses that
				// aren't method calls (on recv).
				if recvIsPrivileged {
					pass.Reportf(n.Pos(), "privileged method %s accesses %s without holding mutex", name, field)
				}
			}
		}

		for _, succ := range b.Succs {
			e := edge{b.Index, succ.Index, lockHeld}
			if _, ok := visited[e]; ok {
				continue
			}
			visited[e] = struct{}{}
			checkPath(succ, lockHeld)
		}
	}
	checkPath(cfgs.FuncDecl(fd).Blocks[0], false)
}

// managesOwnLocking returns whether a method manages its own locking.
func managesOwnLocking(name string) bool {
	return ast.IsExported(name) ||
		firstWordIs(name, "managed") ||
		firstWordIs(name, "threaded") ||
		firstWordIs(name, "call")
}

// firstWordIs returns true if name begins with prefix, followed by an uppercase
// letter. For example, firstWordIs("startsUpper", "starts") == true, but
// firstWordIs("starts", "starts") == false.
func firstWordIs(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(name, prefix)
	return len(suffix) > 0 && ast.IsExported(suffix)
}
