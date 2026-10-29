package gen_test

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-kanna/internal/cli"
)

// toPBs calls ToPB before it is generated, as a slice of what a declared
// function maps is mapped by hand.
const toPBs = `package app

func ToPBs(es []Employee) []*EmployeePB {
	out := make([]*EmployeePB, 0, len(es))
	for _, e := range es {
		out = append(out, ToPB(e))
	}
	return out
}
`

// handler calls ToPB from another package.
const handler = `package handler

import app "example.com/app"

func Send(e app.Employee) string {
	return app.ToPB(e).Name
}
`

func TestCommand_GeneratesWhatCodeAlreadyCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		args  []string
	}{
		{name: "in the package", files: map[string]string{"list.go": toPBs}},
		{name: "in another package", files: map[string]string{"handler/handler.go": handler}, args: []string{"./..."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := map[string]string{"types.go": typesSrc, "mapper.go": mapperSrc}
			maps.Copy(files, tt.files)
			dir := writeModule(t, files)
			if code, stdout, stderr := run(t, dir, tt.args...); code != cli.ExitOK || stdout != "" || stderr != "" {
				t.Fatalf("run = %d, stdout %q, stderr %q; want success", code, stdout, stderr)
			}
			compiles(t, dir)
		})
	}
}

func TestCommand_FollowsADeclarationCodeAlreadyFollows(t *testing.T) {
	t.Parallel()

	// The caller is changed along with the declaration, before the run, while
	// mapper_gen.go still declares ToPB as it was.
	tests := []struct {
		name, decl, use string
	}{
		{"an error added", "ToPB(Employee) (*EmployeePB, error)", "pb, err := ToPB(e)\n\t_ = err\n\treturn pb.Name"},
		{"a pointer taken", "ToPB(*Employee) *EmployeePB", "return ToPB(&e).Name"},
		{"renamed", "EmployeeToPB(Employee) *EmployeePB", "return EmployeeToPB(e).Name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, map[string]string{"types.go": typesSrc, "mapper.go": mapperSrc})
			if code, _, stderr := run(t, dir); code != cli.ExitOK {
				t.Fatalf("first run = %d, stderr %q", code, stderr)
			}
			writeFile(t, filepath.Join(dir, "mapper.go"), strings.Replace(mapperSrc, "ToPB(Employee) *EmployeePB", tt.decl, 1))
			writeFile(t, filepath.Join(dir, "use.go"), "package app\n\nfunc use(e Employee) string {\n\t"+tt.use+"\n}\n")

			if code, _, stderr := run(t, dir); code != cli.ExitOK || stderr != "" {
				t.Fatalf("run = %d, stderr %q; want success", code, stderr)
			}
			compiles(t, dir)
		})
	}
}

func TestCommand_Mistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			// Before ToPB is written, go/types checks no further than
			// undefined: ToPB.
			name: "a mistake behind a call to what is generated",
			files: map[string]string{
				"use.go": "package app\n\nfunc f(e Employee) int { return ToPB(e).Bogus }\n\n" +
					"func g(e Employee) { pb := ToPB(e); pb.Bogus() }\n",
			},
			want: "./use.go:3:41: error: ToPB(e).Bogus undefined (type *EmployeePB has no field or method Bogus)\n" +
				"./use.go:5:40: error: pb.Bogus undefined (type *EmployeePB has no field or method Bogus)\n",
		},
		{
			name:  "an error not about what is generated",
			files: map[string]string{"list.go": toPBs, "broken.go": "package app\n\nvar _ int = \"one\"\n"},
			want: "./broken.go:3:13: error: cannot use \"one\" (untyped string constant) as int value " +
				"in variable declaration\n",
		},
		{
			name: "an error alongside one of generation",
			files: map[string]string{
				"mapper.go": strings.Replace(mapperSrc, "\tnetip.ParseAddr,\n", "", 1),
				"list.go":   toPBs,
				"broken.go": "package app\n\nfunc g() int { return \"s\" }\n",
			},
			want: "./broken.go:3:23: error: cannot use \"s\" (untyped string constant) as int value in return statement\n" +
				"./mapper.go:8:2: error: FromPB: cannot map app.EmployeePB.Id (string) to app.Employee.ID (netip.Addr)\n" +
				"  add a func(string) netip.Addr to a //kanna:converter list\n",
		},
		{
			// A list cannot name a function the run writes either.
			name: "a list naming what is generated",
			files: map[string]string{
				"mapper.go": strings.Replace(mapperSrc, "\tnetip.Addr.String,\n", "\tnetip.Addr.String,\n\tFromPB,\n", 1),
			},
			want: "./mapper.go:15:2: error: undefined: FromPB\n",
		},
		{
			// What cannot map an invalid type is left to the type error.
			name: "a declaration that does not type-check",
			files: map[string]string{
				"types.go": strings.Replace(typesSrc, "ID   netip.Addr", "ID   Foo", 1),
				"list.go":  toPBs,
			},
			want: "./types.go:3:8: error: \"net/netip\" imported and not used\n./types.go:6:7: error: undefined: Foo\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := map[string]string{"types.go": typesSrc, "mapper.go": mapperSrc}
			maps.Copy(files, tt.files)
			dir := writeModule(t, files)
			if code, _, stderr := run(t, dir); code != cli.ExitFailure || stderr != tt.want {
				t.Errorf("run = %d, stderr:\n%s\nwant %d and\n%s", code, stderr, cli.ExitFailure, tt.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "mapper_gen.go")); err == nil {
				t.Error("mapper_gen.go was written")
			}
		})
	}
}

func TestCommand_KeepsWhatCodeStillCalls(t *testing.T) {
	t.Parallel()

	const use = "package app\n\nfunc use(e Employee) string { return ToPB(e).Name }\n"
	tests := []struct {
		name, file, src, edit string
		args                  []string
		want                  string
	}{
		{name: "the directive goes", file: "use.go", src: use, edit: "//kanna:mapper\n",
			want: "./use.go:3:38: error: undefined: ToPB\n"},
		{name: "a function goes", file: "use.go", src: use, edit: "\tToPB(Employee) *EmployeePB\n",
			want: "./use.go:3:38: error: undefined: ToPB\n"},
		{name: "a caller in another package", file: "handler/handler.go", src: handler, edit: "//kanna:mapper\n",
			args: []string{"./..."}, want: "handler/handler.go:6:13: error: undefined: app.ToPB\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, map[string]string{"types.go": typesSrc, "mapper.go": mapperSrc, tt.file: tt.src})
			if code, _, stderr := run(t, dir, tt.args...); code != cli.ExitOK {
				t.Fatalf("first run = %d, stderr %q", code, stderr)
			}
			generated := readFile(t, filepath.Join(dir, "mapper_gen.go"))

			writeFile(t, filepath.Join(dir, "mapper.go"), strings.Replace(mapperSrc, tt.edit, "", 1))
			if code, _, stderr := run(t, dir, tt.args...); code != cli.ExitFailure || stderr != tt.want {
				t.Errorf("run = %d, stderr:\n%s\nwant %d and\n%s", code, stderr, cli.ExitFailure, tt.want)
			}
			if got := readFile(t, filepath.Join(dir, "mapper_gen.go")); got != generated {
				t.Errorf("mapper_gen.go was rewritten:\n%s", got)
			}
		})
	}
}

func TestCommand_RegeneratesOutputCodeCalls(t *testing.T) {
	t.Parallel()

	// Either change empties the last run's output, whose functions the caller
	// keeps calling.
	tests := []struct {
		name string
		edit func(t *testing.T, dir string)
		want string
	}{
		{
			name: "a type renamed",
			edit: func(t *testing.T, dir string) {
				for _, name := range []string{"types.go", "mapper.go"} {
					path := filepath.Join(dir, name)
					writeFile(t, path, strings.ReplaceAll(readFile(t, path), "EmployeePB", "EmployeeWire"))
				}
			},
		},
		{
			name: "a helper's name taken by hand",
			edit: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "zz.go"),
					"package app\n\nfunc addressToAddressPB(a Address) string { return a.City }\n")
			},
			want: "func addressToAddressPB2(",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeModule(t, map[string]string{"types.go": typesSrc, "mapper.go": mapperSrc})
			if code, _, stderr := run(t, dir); code != cli.ExitOK {
				t.Fatalf("first run = %d, stderr %q", code, stderr)
			}
			writeFile(t, filepath.Join(dir, "use.go"),
				"package app\n\nfunc use(e Employee) string {\n\treturn ToPB(e).Name\n}\n")
			tt.edit(t, dir)

			if code, _, stderr := run(t, dir); code != cli.ExitOK || stderr != "" {
				t.Fatalf("run = %d, stderr %q; want success", code, stderr)
			}
			compiles(t, dir)
			if got := readFile(t, filepath.Join(dir, "mapper_gen.go")); !strings.Contains(got, tt.want) {
				t.Errorf("mapper_gen.go lacks %q:\n%s", tt.want, got)
			}
		})
	}
}
