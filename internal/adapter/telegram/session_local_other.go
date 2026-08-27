//go:build !linux

package telegram

func validateLocalFilesystem(string) error {
	return nil
}
