// Package columnarsqlengine is a small, dependency-free Go library: Embeddable columnar SQL query engine in Go with vectorized batch execution, a rule-based optimizer, CSV tables and three-valued NULL logic.
//
// The public API is every exported identifier of this root package. Functions are pure: they do
// no I/O, keep no global mutable state and report invalid input through returned errors.
package columnarsqlengine
