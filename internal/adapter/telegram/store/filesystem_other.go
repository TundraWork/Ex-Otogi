//go:build !linux

package store

func validateLocalFilesystem(string) error {
	return nil
}
