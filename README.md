# Variable Format

`variable-format` is a
[go/analysis](https://pkg.go.dev/golang.org/x/tools/go/analysis)-based tool that
identifies non-constant strings used as the format parameter to a printf-like
function. There are legitimate use cases for this, so seeing this does not
inherently mean you have bad code.

However, if you're trying to track down in a large codebase where `%!x(MISSING)`
is coming from, this analyzer can help you find it.
esults are flaws.

[![CI](https://github.com/mutility/variable-format/actions/workflows/build.yaml/badge.svg)](https://github.com/mutility/variable-format/actions/workflows/build.yaml)

## Example messages

Given the following source code `example.go`:

```go
     1	package example
     2
     3	import (
     4		"fmt"
     5		"os"
     6	)
     7
     8	func main() {
     9	    fmt.Printf(os.Args[0], "Usage", "...")
    10	}
```

variable-format will report the following:

```console
$t variable-format ./...
.../example.go:9:13: variable `os.Args[0]` used for fmt.Printf format parameter
exit status 3
```

## Usage

Run from source with `go run github.com/mutility/variable-format@latest` or
install with `go install github.com/mutility/variable-format@latest` and run
variable-format from GOPATH/bin.

You can configure behvior at the command line by passing the flags below, or in
library use by setting fields on `varfmt.Analyzer()`.

Flag | Field | Meaning
-|-|-
`-no-args` | SuppressNoArgs | Suppress reports of variable formats with no subsequent arguments
`-funcs` | see [printf.Analyzer](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/printf) | Treat additional functions as printf-like

## Bug reports and feature contributions

`variable-format` is developed in spare time, so while bug reports and feature
contributions are welcomed, it may take a while for them to be reviewed. If
possible, try to find a minimal reproduction before reporting a bug. Bugs that
are difficult or impossible to reproduce will likely be closed.

All bug fixes will include tests to help ensure no regression; correspondingly
all contributions should include such tests.

## Mutility Analyzers

`variable-format` is part of [mutility-analyzers](https://github.com/mutility/analyzers).
