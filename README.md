# columnar-sql-engine

[![Go Reference](https://pkg.go.dev/badge/github.com/Trysac/u409-columnar-sql-engine.svg)](https://pkg.go.dev/github.com/Trysac/u409-columnar-sql-engine)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![CI](https://github.com/Trysac/u409-columnar-sql-engine/actions/workflows/ci.yml/badge.svg)](https://github.com/Trysac/u409-columnar-sql-engine/actions/workflows/ci.yml)

Embeddable columnar SQL query engine in Go with vectorized batch execution, a rule-based optimizer, CSV tables and three-valued NULL logic.

## Problem

Describe in two or three sentences the concrete problem this utility solves, who hits it, and what
they do today without it. Replace this paragraph when the first real feature lands.

## Install

```sh
go get github.com/Trysac/u409-columnar-sql-engine
```

Requires Go 1.23 or newer. No dependencies outside the standard library.

## Usage

```go
package main

import (
	"errors"
	"fmt"

	"github.com/Trysac/u409-columnar-sql-engine"
)

func main() {
	// Normalize trims surrounding whitespace and lower-cases the text.
	out, _ := columnarsqlengine.Normalize("  Hello World ")
	fmt.Println(out) // -> "hello world"

	// Blank input is rejected explicitly instead of returning an empty string.
	if _, err := columnarsqlengine.Normalize("   "); errors.Is(err, columnarsqlengine.ErrBlank) {
		fmt.Println("rejected:", err)
	}
}
```

## Documentation

- [Architecture](docs/ARCHITECTURE.md) and [decisions](docs/decisions/)
- [Testing](docs/TESTING.md) and [security](docs/SECURITY.md)
- [Project record](docs/PROJECT.md) and [stack](docs/STACK.md)
- [Changelog](CHANGELOG.md) and [contributing guide](CONTRIBUTING.md)

## License

[MIT](LICENSE) (c) 2026 Miguel Gomez
