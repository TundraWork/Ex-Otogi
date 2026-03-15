// Package eventcache provides an in-memory event cache module that records
// per-article event history, maintains projected article state, exposes lookup
// services to other modules, and handles `~raw` and `~history` command events
// with cached projections.
package eventcache
