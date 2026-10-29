package gen

import (
	"flag"
	"fmt"
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
	set, err := packages.Load(patterns, pc)
	if err != nil {
		return nil, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}
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
		// Code calling a function the run writes does not compile before it
		// has run, nor does code calling it as the run is about to change it.
		// So a type error of a package loaded does not stop the run here:
		// packages.Recheck tells, once the output is in place, whether it
		// remains.
		blocking, deferred := set.Split()
		if diag.HasErrors(blocking) {
			return nil, append(blocking, set.SetAsideWarnings()...)
		}
		changes, missing, ds := generate(set, targets)
		if len(missing) == 0 {
			if diag.HasErrors(ds) {
				return nil, stopped(set, targets, deferred, ds)
			}
			if rds := packages.Recheck(set, changes); len(rds) > 0 {
				return nil, slices.Concat(ds, rds, set.SetAsideWarnings())
			}
			return changes, noTarget(set, targets, changes, ds, patterns)
		}
		if slices.ContainsFunc(missing, func(p string) bool { return slices.Contains(extra, p) }) {
			return nil, []diag.Diag{diag.Errorf(token.Position{}, "cannot read the lists of %s",
				strings.Join(missing, ", "))}
		}
		extra = append(extra, missing...)
		if len(patterns) == 0 {
			patterns = []string{"."}
		}
		if set, err = packages.Load(append(slices.Clone(patterns), extra...), pc); err != nil {
			return nil, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}
		}
	}
}

// stopped returns what a run that cannot generate reports: its own errors,
// and the type errors it deferred, as one may be the cause, but for those that
// say only that a function it writes is missing. One in a //kanna:converter
// list stays, as a list cannot name such a function either. What does not
// type-check is left out of its own errors, as the type error behind it says
// more; should that leave no error, all are reported.
func stopped(set packages.Set, targets map[string]bool, deferred, ds []diag.Diag) []diag.Diag {
	written := make(map[string]bool)
	for _, pkg := range set.Packages {
		if targets[pkg.PkgPath] {
			decls, _ := decls(pkg)
			for _, d := range decls {
				written[pkg.PkgPath+"."+d.name] = true
			}
		}
	}
	pending, listed := set.Pending(written), inLists(set)
	reported := slices.Concat(
		slices.DeleteFunc(slices.Clone(deferred), func(d diag.Diag) bool { return pending(d) && !listed(d) }),
		slices.DeleteFunc(slices.Clone(ds), func(d diag.Diag) bool { return d.Message == notTypeChecked }),
	)
	if !diag.HasErrors(reported) {
		return slices.Concat(deferred, ds)
	}
	return reported
}

// noTarget warns that a run found nothing to generate: a go:generate line left
// behind does no harm, but says nothing either. The packages of set that
// targets names are named by import path, since go generate runs each in its
// own directory, where "." would name them all alike; the patterns only when
// they matched none.
func noTarget(set packages.Set, targets map[string]bool, changes []output.Change, diags []diag.Diag,
	patterns []string,
) []diag.Diag {
	if diag.HasErrors(diags) || slices.ContainsFunc(changes, func(c output.Change) bool { return !c.Remove }) {
		return diags
	}
	var (
		paths []string
		lists bool
	)
	for _, pkg := range set.Packages {
		if targets[pkg.PkgPath] {
			paths = append(paths, pkg.PkgPath)
			lists = lists || hasLists(pkg)
		}
	}
	in := strings.Join(paths, ", ")
	if len(paths) == 0 {
		in = strings.Join(patterns, " ")
	}
	d := diag.Warningf(token.Position{}, "no interface is marked //kanna:%s in %s", mapperKey, in)
	if lists {
		d = d.WithHints(fmt.Sprintf("a package holding only //kanna:%s lists needs no //go:generate line;"+
			" the packages naming its lists read them", converterKey))
	}
	return append(diags, d)
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
		plans, pd := planFuncs(pkg, ds, cs, declared)
		if diags = append(append(diags, cd...), pd...); diag.HasErrors(slices.Concat(dd, cd, pd)) {
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
