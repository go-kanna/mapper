package gen

import (
	"flag"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/output"
	"github.com/go-kanna/internal/packages"
)

// outputFile is the file generated in each package.
const outputFile = "mapper_gen.go"

// config holds what kanna-mapper's own flags set.
type config struct {
	tags string
}

// Command returns kanna-mapper.
func Command() cli.Command[config] {
	return cli.Command[config]{
		Name:    generator,
		Summary: "generate the functions declared by interfaces marked //kanna:" + mapperKey,
		Args:    "[packages]",
		Help: "The packages default to the one in the current directory, so that\n" +
			"//go:generate go tool " + generator + " needs no argument.",
		Flags: func(fs *flag.FlagSet, cfg *config) {
			fs.StringVar(&cfg.tags, "tags", "", "comma-separated build tags")
		},
		Run: run,
	}
}

func run(dir string, cfg config, patterns []string) ([]output.Change, []diag.Diag) {
	pc := packages.Config{Dir: dir, Tags: cfg.tags, TypesInfo: true, Regenerated: []string{outputFile}}
	set, diags, ok := load(patterns, pc)
	if !ok {
		return nil, diags
	}
	targets := make(map[string]bool, len(set.Packages))
	for _, pkg := range set.Packages {
		targets[pkg.PkgPath] = true
	}

	// A list another package declares is read from its syntax, which a package
	// loaded only as an import does not have. Its package is then loaded too,
	// alongside the others: the types of two loads are never identical.
	var extra []string
	for {
		changes, missing, ds := generate(set, targets)
		if len(missing) == 0 {
			return changes, append(diags, noTarget(changes, ds, patterns)...)
		}
		if slices.ContainsFunc(missing, func(p string) bool { return slices.Contains(extra, p) }) {
			return nil, append(diags, diag.Errorf(token.Position{}, "cannot read the lists of %s",
				strings.Join(missing, ", ")))
		}
		extra = append(extra, missing...)
		if len(patterns) == 0 {
			patterns = []string{"."}
		}
		if set, diags, ok = load(append(slices.Clone(patterns), extra...), pc); !ok {
			return nil, diags
		}
	}
}

// load loads patterns, reporting false when what it loaded has errors. The
// regenerated files it had to set aside are pointed out then.
func load(patterns []string, pc packages.Config) (packages.Set, []diag.Diag, bool) {
	set, err := packages.Load(patterns, pc)
	if err != nil {
		return set, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}, false
	}
	if !diag.HasErrors(set.Diags) {
		return set, set.Diags, true
	}
	for _, path := range set.SetAside {
		set.Diags = append(set.Diags, diag.Warningf(token.Position{Filename: path},
			"set aside because it no longer compiles; it is regenerated once the errors above are fixed"))
	}
	return set, set.Diags, false
}

// noTarget warns that a run found nothing to generate: a go:generate line left
// behind does no harm, but says nothing either.
func noTarget(changes []output.Change, diags []diag.Diag, patterns []string) []diag.Diag {
	if diag.HasErrors(diags) || slices.ContainsFunc(changes, func(c output.Change) bool { return !c.Remove }) {
		return diags
	}
	in := strings.Join(patterns, " ")
	if in == "" {
		in = "."
	}
	return append(diags, diag.Warningf(token.Position{}, "no interface is marked //kanna:%s in %s", mapperKey, in))
}

// generate works out the changes for the packages of set that targets names:
// the output of each package declaring functions, and the removal of what an
// earlier run left in a package declaring none. It also returns the packages
// whose lists are named but were not loaded with syntax, for the caller to
// load and call again.
func generate(set packages.Set, targets map[string]bool) ([]output.Change, []string, []diag.Diag) {
	loaded := make(map[string]*packages.Package, len(set.Packages))
	for _, pkg := range set.Packages {
		loaded[pkg.PkgPath] = pkg
	}

	var (
		changes []output.Change
		missing []string
		diags   []diag.Diag
	)
	for _, pkg := range set.Packages {
		if !targets[pkg.PkgPath] || len(pkg.GoFiles) == 0 {
			continue
		}
		dir := filepath.Dir(pkg.GoFiles[0])
		path := filepath.Join(dir, outputFile)

		ds, dd := decls(pkg)
		diags = append(diags, dd...)
		if len(ds) == 0 {
			// A package whose interfaces have all lost the directive keeps the
			// functions an earlier run wrote unless the file goes as well.
			if !diag.HasErrors(dd) && packages.GeneratedFiles(pkg)[path] {
				changes = append(changes, output.Change{Path: path, Remove: true})
			}
			continue
		}

		cs, miss, cd := convsOf(pkg, loaded, ds)
		if missing = append(missing, miss...); len(miss) > 0 {
			continue
		}
		declared := output.DeclaredNames(dir, outputFile, pkg.Name)
		plans, pd := planFuncs(pkg.Fset, ds, cs, declared)
		if diags = append(append(diags, cd...), pd...); diag.HasErrors(diags) {
			continue
		}

		f := emit(pkg.PkgPath, pkg.Name, plans, slices.Sorted(maps.Keys(declared)))
		src, err := f.Bytes()
		if err != nil {
			diags = append(diags, diag.Errorf(token.Position{Filename: path}, "generated code does not parse: %v", err))
			continue
		}
		changes = append(changes, output.Change{Path: path, Data: src})
		diags = append(diags, output.MissingRequires(path, f.ModuleImports())...)
	}
	slices.Sort(missing)
	return changes, slices.Compact(missing), diags
}
