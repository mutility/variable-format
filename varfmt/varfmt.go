// Package varfmt implements the variable-format analyzer. It reports when a
// non-const, non-literal is passed as the format string to a Printf-like
// function.

package varfmt

import (
	"fmt"
	"go/ast"
	"go/printer"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/ast/inspector"
)

const doc = `varfmt reports variables passed as format strings in printf-like functions

While this isn't necessarily a problem, and sometimes is very intentional,
accidental use of potentially unvetted strings can result in ugly tokens like
%!x(MISSING) showing up in the middle of your formatted strings. Varfmt reports
all uses of variables as format strings, except those that are merely pass-
throughs in a wrapper of a printf-like function.`

type varfmtAnalyzer struct {
	*analysis.Analyzer
	SuppressNoArgs bool
}

func Analyzer() *varfmtAnalyzer {
	v := &varfmtAnalyzer{
		Analyzer: &analysis.Analyzer{
			Name:     "varfmt",
			Doc:      doc,
			Requires: []*analysis.Analyzer{inspect.Analyzer, printf.Analyzer},
		},
	}
	v.Flags.BoolVar(&v.SuppressNoArgs, "no-args", false, "suppress varfmt reports when formatted args are passed")

	// allow overriding printf flags
	funcs := printf.Analyzer.Flags.Lookup("funcs")
	v.Flags.Var(funcs.Value, funcs.Name, funcs.Usage)

	v.Run = v.run

	return v
}

func (v *varfmtAnalyzer) run(pass *analysis.Pass) (interface{}, error) {
	pp = func(x ast.Node) string {
		var sb strings.Builder
		printer.Fprint(&sb, pass.Fset, x)
		return sb.String()
	}

	// track objects of parameters that are themselves format strings so as to
	// ignore them when their variable is passed a format string.
	passthru := make(map[types.Object]struct{})

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	printers := pass.ResultOf[printf.Analyzer].(*printf.Result)

	callFilter := []ast.Node{
		(*ast.CallExpr)(nil),
		(*ast.FuncDecl)(nil),
		// (*ast.FuncLit)(nil), // Can we handle a printf-wrapper FuncLit?
		(*ast.AssignStmt)(nil),
	}
	inspect.Preorder(callFilter, func(n ast.Node) {
		switch n := n.(type) {
		case *ast.FuncDecl: // validate wrapper pass-through
			obj := pass.TypesInfo.ObjectOf(n.Name)
			if obj != nil {
				if i := fmtarg(printers, obj); i >= 0 {
					id := nthName(n.Type.Params.List, i)
					if id != nil {
						obj := pass.TypesInfo.ObjectOf(id)
						if obj != nil {
							passthru[obj] = struct{}{}
						}
					}
				}
			}
		case *ast.AssignStmt: // invalidate wrapper pass-through
			for _, lh := range n.Lhs {
				if id, ok := lh.(*ast.Ident); ok {
					obj := pass.TypesInfo.ObjectOf(id)
					if obj != nil {
						delete(passthru, obj)
					}
				}
			}
		case *ast.CallExpr:
			checkarg := -1
			switch callfun := n.Fun.(type) {
			case *ast.Ident:
				obj := pass.TypesInfo.ObjectOf(callfun)
				checkarg = fmtarg(printers, obj)
			case *ast.SelectorExpr:
				obj := selobj(pass.TypesInfo, callfun)
				checkarg = fmtarg(printers, obj)
			case *ast.CallExpr, *ast.TypeAssertExpr, *ast.ParenExpr, *ast.IndexExpr:
			case *ast.FuncLit, *ast.ArrayType, *ast.InterfaceType, *ast.MapType, *ast.ChanType, *ast.StructType:
			default:
				pt("unhandled callfun "+pp(n.Fun), n.Fun)
				return
			}

			if checkarg < 0 {
				return
			}
			if v.SuppressNoArgs && len(n.Args)-1 > checkarg {
				return
			}

			switch arg := n.Args[checkarg].(type) {
			case *ast.Ident:
				switch obj := pass.TypesInfo.ObjectOf(arg).(type) {
				case *types.Var:
					// ignore validated wrapper pass-through
					if _, ok := passthru[obj]; !ok {
						pass.Report(analysis.Diagnostic{
							Pos:     arg.Pos(),
							End:     arg.End(),
							Message: fmt.Sprintf("variable `%s` used for %s format parameter", pp(arg), pp(n.Fun)),
							Related: []analysis.RelatedInformation{{
								Pos:     obj.Pos(),
								Message: "defined here",
							}},
						})
					}
				case *types.Const:
				default:
					pt("unhandled argid "+pp(arg), obj)
				}

			default:
				bad := make(map[ast.Node]struct{})
				if !constlit(pass.TypesInfo, arg, bad) {
					// fmt.Printf("%0.20q math formatting contains %v\n", pp(n), bad)
					var related []analysis.RelatedInformation
					for x := range bad {
						related = append(related, analysis.RelatedInformation{
							Pos:     x.Pos(),
							End:     x.End(),
							Message: fmt.Sprintf("Non-constant: %s", pp(x)),
						})
					}
					msg := "non-constant expression"
					if len(bad) == 1 {
						for x := range bad {
							if s := pp(x); len(s) < 32 {
								msg = "variable `" + pp(x) + "`"
							}
						}
					}
					pass.Report(analysis.Diagnostic{
						Pos:     arg.Pos(),
						End:     arg.End(),
						Message: fmt.Sprintf("%s used for %s format parameter", msg, pp(n.Fun)),
						Related: related,
					})
				}
			}
		}
	})
	return nil, nil
}

func fmtarg(printers *printf.Result, obj types.Object) int {
	fun, ok := obj.(*types.Func)
	if !ok {
		return -1
	}
	switch printers.Kind(fun) {
	case printf.KindPrintf, printf.KindErrorf:
		sig := fun.Type().(*types.Signature)
		params := sig.Params()
		return params.Len() - 2 // assumes func(⋯, format, args...)
	default: // includes printf.KindNone, printf.KindPrint
		return -1
	}
}

func constlit(ti *types.Info, x ast.Expr, bad map[ast.Node]struct{}) bool {
	switch x := x.(type) {
	case *ast.BinaryExpr:
		return combine(ti, x, bad, x.X, x.Y)
	case *ast.KeyValueExpr:
		return combine(ti, x, bad, x.Key, x.Value)
	case *ast.ParenExpr:
		return combine(ti, x, bad, x.X)
	case *ast.SliceExpr:
		exprs := append(make([]ast.Expr, 0, 4), x.X)
		if x.Low != nil {
			exprs = append(exprs, x.Low)
		}
		if x.High != nil {
			exprs = append(exprs, x.High)
		}
		if x.Max != nil {
			exprs = append(exprs, x.Max)
		}
		return combine(ti, x, bad, exprs...)
	case *ast.StarExpr:
		return combine(ti, x, bad, x.X)
	case *ast.TypeAssertExpr:
		return combine(ti, x, bad, x.X)
	case *ast.UnaryExpr:
		return combine(ti, x, bad, x.X)
	case *ast.SelectorExpr:
		if o := selobj(ti, x); o != nil && constobj(o) {
			return true
		}

	case *ast.BasicLit:
		return true
	case *ast.Ident:
		if constobj(ti.ObjectOf(x)) {
			return true
		}

	case *ast.CallExpr:
		if len(x.Args) == 1 && typeexpr(ti, x.Fun) && constlit(ti, x.Args[0], bad) {
			return true
		}

	case *ast.IndexExpr:

	default:
		pt("unhandled constlit "+pp(x), x)
	}

	bad[x] = struct{}{}
	return false
}

func combine(ti *types.Info, parent ast.Expr, bad map[ast.Node]struct{}, xs ...ast.Expr) bool {
	allgood := true
	allbad := true
	for _, x := range xs {
		if constlit(ti, x, bad) {
			allbad = false
		} else {
			allgood = false
		}
	}
	if allbad {
		for _, x := range xs {
			delete(bad, x)
		}
		bad[parent] = struct{}{}
	}
	return allgood
}

func pt(label string, x interface{}) {
	println(label, reflect.TypeOf(x).String())
}

var pp func(ast.Node) string

func constobj(obj types.Object) bool {
	_, ok := obj.(*types.Const)
	return ok
}

func typeobj(obj types.Object) bool {
	_, ok := obj.(*types.TypeName)
	return ok
}

func typeexpr(ti *types.Info, x ast.Expr) bool {
	if sel, ok := x.(*ast.SelectorExpr); ok {
		x = sel.Sel
	}
	if id, ok := x.(*ast.Ident); ok {
		return typeobj(ti.ObjectOf(id))
	}
	pt("typeexpr "+pp(x), x)
	return false
}

func nthName(flds []*ast.Field, nth int) *ast.Ident {
	i := 0
	for _, fld := range flds {
		for _, n := range fld.Names {
			if i == nth {
				return n
			}
			i++
		}
	}
	return nil
}

func selobj(ti *types.Info, x *ast.SelectorExpr) types.Object {
	if obj := ti.ObjectOf(x.Sel); obj != nil {
		return obj
	}
	sel := ti.Selections[x]
	return sel.Obj()
}
