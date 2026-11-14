package app

import (
	"io/fs"
	"strings"
)

func fsGlobUp(f fs.FS) (int, error) {
	ents, err := fs.ReadDir(f, ".")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			n++
		}
	}
	return n, nil
}
