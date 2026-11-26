//go:build !unix

package store

import "os"

func preserveStateGroup(*os.File, os.FileInfo) error { return nil }
