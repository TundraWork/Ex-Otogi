// Package undocumentedembedded is a fixture.
package undocumentedembedded

// Container demonstrates embedded field documentation checks.
type Container struct {
	*Exported
}

// Exported is fixture data.
type Exported struct{}
