// Package lintererrors is a fixture.
package lintererrors

import (
	"errors"
	"fmt"
	"os"
)

// Broken demonstrates representative static-analysis failures.
func Broken() error {
	os.Chdir("/")
	return fmt.Errorf("broken: %v", errors.New("cause"))
}
