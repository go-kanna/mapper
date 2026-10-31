package gen

import (
	"path/filepath"

	"github.com/go-kanna/internal/deps"
	"github.com/go-kanna/internal/packages"
)

// reportDeps tells what a run in dir reads and writes.
func reportDeps(dir string, cfg config, patterns []string) (deps.Report, error) {
	set, err := packages.Load(patterns, packages.Config{
		Dir: dir, Tags: cfg.tags, TypesInfo: true, Regenerated: []string{outputFile},
	})
	if err != nil {
		return deps.Report{}, err //nolint:wrapcheck // it says what could not be loaded
	}

	r := deps.Report{Packages: set.Paths()}
	for _, pkg := range set.Packages {
		found, _ := decls(pkg)
		if len(found) == 0 || len(pkg.GoFiles) == 0 {
			continue
		}
		r.Writes = append(r.Writes, filepath.Join(filepath.Dir(pkg.GoFiles[0]), outputFile))
		for _, d := range found {
			r.Declares = append(r.Declares, pkg.PkgPath+"."+d.name)
		}
	}
	return r, nil
}
