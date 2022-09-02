// Package varfmt implements the variable-format analyzer. It reports when a
// non-const, non-literal is passed as the format string to a Printf-like
// function.

package varfmt

import (
	"fmt"
	"go/ast"
	"go/printer"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/ssa"
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
			Requires: []*analysis.Analyzer{buildssa.Analyzer, printf.Analyzer},
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
	pp := func(x ast.Node) string {
		var sb strings.Builder
		printer.Fprint(&sb, pass.Fset, x)
		return sb.String()
	}

	prog := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)
	printers := pass.ResultOf[printf.Analyzer].(*printf.Result)

	for _, fn := range prog.SrcFuncs {
		if tfun, ok := fn.Object().(*types.Func); ok {
			var fmtParam *ssa.Parameter
			if printers.Kind(tfun) != printf.KindNone {
				if len(fn.Params) >= 2 {
					fmtParam = fn.Params[len(fn.Params)-2]
				}
			}
			for _, blk := range fn.Blocks {
				for _, inst := range blk.Instrs {
					if call, ok := inst.(ssa.CallInstruction); ok {
						com := call.Common()
						if com.IsInvoke() {
							pass.Reportf(com.Pos(), "invoke")
						} else {
							callee := com.StaticCallee()
							if callee == nil {
								continue
							}

							if tfun, ok := callee.Object().(*types.Func); ok {
								if printers.Kind(tfun) == printf.KindNone {
									continue
								}
								if len(com.Args) < 2 {
									continue
								}

								msg := "non-constant expression"

								fmtarg := com.Args[len(com.Args)-2]
								switch v := fmtarg.(type) {
								case *ssa.Const:
									continue // Constants are fine
								case *ssa.Slice:
									if _, ok := v.X.(*ssa.Const); ok {
										continue // Slices of const strings are ok
									}
								case *ssa.Parameter:
									if v == fmtParam {
										continue // pass-through format params are ok.
									}
								}

								var n ast.Node
								for _, f := range pass.Files {
									if f.Pos() <= call.Pos() && f.End() >= call.Pos() {
										path, _ := astutil.PathEnclosingInterval(f, call.Pos(), call.Pos())
										for _, p := range path {
											if c, ok := p.(*ast.CallExpr); ok {
												if len(c.Args) < len(com.Args)-2 {
													break
												}
												n = c.Args[len(com.Args)-2]
											}
										}
									}
								}

								m := fmt.Sprintf("variable `%s`", pp(n))
								if len(m) < 2*len(msg) {
									msg = m
								}

								name := tfun.FullName()
								if tfun.Pkg() == pass.Pkg {
									name = strings.TrimPrefix(name, pass.Pkg.Name()+".")
								}
								pass.Reportf(call.Pos(), "%s used for %s format parameter", msg, name)
							}
						}
					}
				}
			}
		}
	}

	return nil, nil
}
