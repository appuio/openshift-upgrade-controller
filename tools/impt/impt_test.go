package impt_test

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"testing"

	"golang.org/x/exp/maps"
)

func TestImport(t *testing.T) {
	fset := token.NewFileSet()
	pt := parseWithTypes(fset)
	fmt.Println("Package parsed and type checked", pt.Name())
	fmt.Println("Package path", pt)
}

func parseWithTypes(fset *token.FileSet) *types.Package {
	imp, err := build.Import("github.com/appuio/openshift-upgrade-controller/tools/toimp", "", build.FindOnly)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Import from: ", imp.Dir)
	rawPkgs, err := parser.ParseDir(fset, imp.Dir, nil, parser.SkipObjectResolution)
	if err != nil {
		log.Fatal(err)
	}
	rawPkg := rawPkgs["toimp"]
	info := types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "source", nil),
	}
	pkg, err := conf.Check(imp.Dir, fset, maps.Values(rawPkg.Files), &info)
	if err != nil {
		log.Fatal(err)
	}
	return pkg
}
