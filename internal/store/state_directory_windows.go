package store

import "os"

func makeStateDirectory(path string) error {
	return os.MkdirAll(path, 0755)
}
