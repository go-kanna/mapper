package gen_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-kanna/internal/deps"
)

// Code calling what the run writes does not compile before it, which is when
// the kanna command asks.
func TestDeps(t *testing.T) {
	t.Parallel()

	dir := writeModule(t, map[string]string{
		"p/p.go": "package p\n\ntype A struct{ X int }\n\ntype B struct{ X int }\n\n" +
			"//kanna:mapper\ntype _ interface {\n\tAToB(a A) B\n}\n\nvar _ = AToB(A{})\n",
	})

	code, stdout, stderr := run(t, filepath.Join(dir, "p"), "-deps")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var r deps.Report
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(r.Packages, []string{"example.com/app/p"}) {
		t.Errorf("Packages = %q", r.Packages)
	}
	if len(r.Writes) != 1 || !strings.HasSuffix(r.Writes[0], filepath.Join("p", "mapper_gen.go")) {
		t.Errorf("Writes = %q", r.Writes)
	}
	if !slices.Equal(r.Declares, []string{"example.com/app/p.AToB"}) {
		t.Errorf("Declares = %q", r.Declares)
	}
}
