package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"log"

	"golang.org/x/exp/maps"
)

func main() {
	fset := token.NewFileSet()

	pkg := parseWithTypes(fset)
	fmt.Println("Package parsed and type checked", pkg.Name())

	cvs := pkg.Scope().Lookup("ClusterVersionSpec")
	if cvs == nil {
		log.Fatal("ClusterVersionSpec not found")
	}
	fmt.Println("ClusterVersionSpec found")
	fmt.Println(types.ObjectString(cvs, types.RelativeTo(pkg)))
	str, ok := cvs.Type().Underlying().(*types.Struct)
	if !ok {
		log.Fatal("ClusterVersionSpec is not a struct")
	}

	toExport := extractNamed([]types.Object{cvs}, str)

	fmt.Println("####################")

	for _, obj := range toExport {
		fmt.Println(types.ObjectString(obj, types.RelativeTo(pkg)))
	}

	fmt.Println("####################")

}

func extractNamed(toExport []types.Object, cvs types.Type) []types.Object {
	switch t := cvs.(type) {
	case *types.Named:
		fmt.Println("Named type", t.Obj())
		toExport = append(toExport, t.Obj())
		toExport = extractNamed(toExport, t.Underlying())
	case *types.Basic:
	case *types.Pointer:
		toExport = extractNamed(toExport, t.Elem())
	case *types.Array:
		toExport = extractNamed(toExport, t.Elem())
	case *types.Slice:
		toExport = extractNamed(toExport, t.Elem())
	case *types.Map:
		toExport = extractNamed(toExport, t.Key())
		toExport = extractNamed(toExport, t.Elem())
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			field := t.Field(i)
			fmt.Printf("%s (%T)\n", field.String(), field.Type())
			toExport = extractNamed(toExport, field.Type())
		}
	default:
		log.Fatalf("Type not yet supported %T", t)
	}
	return toExport
}

func parseWithTypes(fset *token.FileSet) *types.Package {
	imp, err := build.Import("github.com/openshift/api/config/v1", "", build.FindOnly)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Import from: ", imp.Dir)
	rawPkgs, err := parser.ParseDir(fset, imp.Dir, nil, parser.SkipObjectResolution)
	if err != nil {
		log.Fatal(err)
	}
	rawPkg := rawPkgs["v1"]
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
