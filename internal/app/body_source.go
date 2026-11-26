package app

import (
	"io"
	"os"
)

type bodyFile interface {
	io.ReadCloser
	Stat() (os.FileInfo, error)
}

var openBodyFileFn = func(path string) (bodyFile, error) {
	return os.Open(path)
}

func readOpenedBodyFile(path string) (string, bool, error) {
	source, err := openBodyFileFn(path)
	if err != nil {
		return "", false, err
	}
	defer source.Close()
	info, statErr := source.Stat()
	regular := statErr == nil && info.Mode().IsRegular()
	data, err := io.ReadAll(source)
	return string(data), regular, err
}
