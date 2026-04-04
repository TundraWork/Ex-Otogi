// Package management defines the public observability contracts for the
// Ex-Otogi management plane.
//
// The package is intentionally implementation-agnostic. It standardizes
// service registry keys, event/artifact/snapshot DTOs, query filters, trace
// context helpers, and the write/read interfaces used by kernel, drivers,
// modules, and providers. Concrete storage, retention, transport, and runtime
// lifecycle logic belong outside pkg/otogi.
package management
