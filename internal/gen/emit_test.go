package gen_test

import (
	"path/filepath"
	"testing"

	"github.com/go-kanna/internal/output"
	"github.com/go-kanna/internal/packages"
	"github.com/go-kanna/internal/pkgtest"

	"github.com/go-kanna/mapper/internal/gen"
)

// TestEmit compares the output for testdata/emit/mapper with its golden file,
// which TestGenerated calls. To accept a change in the output:
//
//	go test ./internal/gen -run TestEmit -update
func TestEmit(t *testing.T) {
	t.Parallel()

	const dir = "testdata/emit/mapper"
	set, err := packages.Load([]string{"./" + dir},
		packages.Config{TypesInfo: true, Regenerated: []string{"mapper_gen.go"}})
	if err != nil || len(set.Diags) > 0 {
		t.Fatalf("load: %v %v", err, set.Diags)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	pkg := set.Packages[0]

	got, diags, err := gen.Emit(pkg, set.Packages, output.DeclaredNames(abs, "mapper_gen.go", pkg.Name))
	if err != nil || len(diags) > 0 {
		t.Fatalf("emit: %v %q\n%s", err, diags, got)
	}
	pkgtest.Golden(t, filepath.Join(dir, "mapper_gen.go"), got)
}
