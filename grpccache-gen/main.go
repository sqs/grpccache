package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"log"
	"os"
	"sort"
	"strings"
)

var (
	filesStr = flag.String("files", "", "pkg@filename entries (space-separated) of pkgs/filenames that define generated server/client types")
	outPkg   = flag.String("pkg", "trace", "output package name")
	outFile  = flag.String("o", "", "output file (default: stdout)")

	fset = token.NewFileSet()
)

// genFile is a generated gRPC file and associated metadata. It is
// parsed using parseFilesStr.
type genFile struct {
	ImportPath string // Go pkg import path
	PBGoFile   string // .pb.go filename
}

func parseFilesStr(filesStr string) []genFile {
	if filesStr == "" {
		log.Fatal("Must specify some -files")
	}
	var files []genFile
	entries := strings.Fields(filesStr)
	for _, e := range entries {
		parts := strings.Split(e, "@")
		files = append(files, genFile{ImportPath: parts[0], PBGoFile: parts[1]})
	}
	return files
}

func main() {
	flag.Parse()
	log.SetFlags(0)

	genFiles := parseFilesStr(*filesStr)

	var genTypes []genType
	for _, f := range genFiles {
		astFile, err := parser.ParseFile(fset, f.PBGoFile, nil, parser.AllErrors)
		if err != nil {
			log.Fatal(err)
		}

		genTypes2 := Types(astFile, func(tspec *ast.TypeSpec) bool {
			_, ok := tspec.Type.(*ast.InterfaceType)
			return ok && strings.HasSuffix(tspec.Name.Name, "Client")
		})
		if len(genTypes2) == 0 {
			log.Printf("warning: file %s has no matching types", f.PBGoFile)
		}

		for _, t := range genTypes2 {
			genTypes = append(genTypes, genType{t, astFile.Name.Name, f.ImportPath})
		}
	}

	src, err := write(genTypes, *outPkg)
	if err != nil {
		log.Fatal(err)
	}

	var w io.Writer
	if *outFile == "" {
		w = os.Stdout
	} else {
		f, err := os.Create(*outFile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		w = f
	}
	if _, err := w.Write(src); err != nil {
		log.Fatal(err)
	}
}

// Types returns all top-level type declarations in fileOrPkg (an
// *ast.File or *ast.Package) for which the filter func returns true.
func Types(fileOrPkg ast.Node, filter func(*ast.TypeSpec) bool) []*ast.TypeSpec {
	var types []*ast.TypeSpec
	ast.Walk(visitFn(func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.GenDecl:
			if node.Tok == token.TYPE {
				for _, spec := range node.Specs {
					tspec := spec.(*ast.TypeSpec)
					if filter(tspec) {
						types = append(types, tspec)
					}
				}
			}
			return false
		default:
			return true
		}
	}), fileOrPkg)
	return types
}

type visitFn func(node ast.Node) (descend bool)

func (v visitFn) Visit(node ast.Node) ast.Visitor {
	descend := v(node)
	if descend {
		return v
	}
	return nil
}

type genType struct {
	*ast.TypeSpec
	pkgName    string
	importPath string
}

func (x genType) typeName() string {
	return x.pkgName + "." + x.Name.Name
}

func (x genType) name() string {
	return strings.TrimSuffix(strings.TrimSuffix(x.Name.Name, "Client"), "Server")
}

func (x genType) clientName() string {
	return x.name() + "Client"
}

func (x genType) serverName() string {
	return x.name() + "Server"
}

func (x genType) clientImplName() string {
	return "Cached" + x.Name.Name
}

func (x genType) serverImplName() string {
	return "Cached" + x.serverName()
}

type genTypeList []genType

func (v genTypeList) Len() int           { return len(v) }
func (v genTypeList) Less(i, j int) bool { return v[i].typeName() < v[j].typeName() }
func (v genTypeList) Swap(i, j int)      { v[i], v[j] = v[j], v[i] }

func (v genTypeList) imports() []string {
	impsMap := map[string]struct{}{}
	for _, ifc := range v {
		impsMap[ifc.importPath] = struct{}{}
	}
	imps := make([]string, 0, len(impsMap))
	for imp := range impsMap {
		imps = append(imps, imp)
	}

	imps = append(imps, "google.golang.org/grpc")
	imps = append(imps, "google.golang.org/grpc/metadata")
	imps = append(imps, "golang.org/x/net/context")
	imps = append(imps, "sourcegraph.com/sqs/grpccache")

	sort.Strings(imps)
	return imps
}

func write(genTypes []genType, outPkg string) ([]byte, error) {
	// Sort for determinism.
	sort.Sort(genTypeList(genTypes))

	var w bytes.Buffer

	fmt.Fprintln(&w, "// GENERATED CODE - DO NOT EDIT!")
	fmt.Fprintln(&w, "//")
	fmt.Fprintln(&w, "// Generated by:")
	fmt.Fprintln(&w, "//")
	fmt.Fprintf(&w, "//   go run gen_trace.go %s\n", strings.Join(os.Args[1:], " "))
	fmt.Fprintln(&w, "//")
	fmt.Fprintln(&w, "// Called via:")
	fmt.Fprintln(&w, "//")
	fmt.Fprintln(&w, "//   go generate")
	fmt.Fprintln(&w, "//")
	fmt.Fprintln(&w)
	fmt.Fprint(&w, "package ", outPkg, "\n")
	fmt.Fprintln(&w)
	fmt.Fprintln(&w, "import (")
	for _, imp := range genTypeList(genTypes).imports() {
		if imp == "sourcegraph.com/sqs/grpccache/testpb" {
			// HACK(sqs): skip self; hardcoded currently
			continue
		}
		fmt.Fprint(&w, "\t", `"`+imp+`"`, "\n")
	}
	fmt.Fprintln(&w, ")")
	fmt.Fprintln(&w)

	// Cached types
	for _, genType := range genTypes {

		{
			// Server
			fmt.Fprintf(&w, "type %s struct { %s }\n", genType.serverImplName(), genType.serverName())
			fmt.Fprintln(&w)

			// Methods
			for _, methField := range genType.Type.(*ast.InterfaceType).Methods.List {
				if meth, ok := methField.Type.(*ast.FuncType); ok {
					synthesizeFieldNamesIfMissing(meth.Params)
					if genType.pkgName != outPkg {
						// TODO(sqs): check for import paths or dirs unequal, not pkg name
						qualifyPkgRefs(meth, genType.pkgName)
					}

					// remove client-only "opts
					// ... grpc.CallOption". Copy it to avoid
					// conflicting with the client codegen below.
					tmp := *meth
					meth = &tmp
					tmp2 := *meth.Params
					meth.Params = &tmp2
					meth.Params.List = meth.Params.List[:2]

					body := astParse(`
ctx, cc := grpccache.Internal_WithCacheControl(ctx)
result, err := s.` + genType.serverName() + `.` + methField.Names[0].Name + `(ctx, in)
if !cc.IsZero() {
	if err := grpccache.Internal_SetCacheControlTrailer(ctx, *cc); err != nil {
		return nil, err
	}
}
return result, err
`)

					decl := &ast.FuncDecl{
						Recv: &ast.FieldList{List: []*ast.Field{
							{
								Names: []*ast.Ident{ast.NewIdent("s")},
								Type:  &ast.StarExpr{X: ast.NewIdent(genType.serverImplName())},
							},
						}},
						Name: ast.NewIdent(methField.Names[0].Name),
						Type: meth,
						Body: &ast.BlockStmt{List: body},
					}
					fmt.Fprintln(&w, astString(decl))
					fmt.Fprintln(&w)
				}
			}
		}

		{
			// Client
			fmt.Fprintf(&w, "type %s struct { %s; Cache *grpccache.Cache }\n", genType.clientImplName(), genType.Name.Name)
			fmt.Fprintln(&w)

			// Methods
			for _, methField := range genType.Type.(*ast.InterfaceType).Methods.List {
				if meth, ok := methField.Type.(*ast.FuncType); ok {
					synthesizeFieldNamesIfMissing(meth.Params)
					if genType.pkgName != outPkg {
						// TODO(sqs): check for import paths or dirs unequal, not pkg name
						qualifyPkgRefs(meth, genType.pkgName)
					}

					key := genType.name() + "." + methField.Names[0].Name
					body := astParse(`
if s.Cache != nil {
	var cachedResult ` + resultType(meth) + `
	cached, err := s.Cache.Get(ctx, "` + key + `", in, &cachedResult)
	if err != nil {
		return nil, err
	}
	if cached {
		return &cachedResult, nil
	}
}

var trailer metadata.MD

result, err := s.` + genType.Name.Name + `.` + methField.Names[0].Name + `(ctx, in, grpc.Trailer(&trailer))
if err != nil {
	return nil, err
}
if s.Cache != nil {
	if err := s.Cache.Store(ctx, "` + key + `", in, result, trailer); err != nil {
		return nil, err
	}
}
return result, nil
`)

					decl := &ast.FuncDecl{
						Recv: &ast.FieldList{List: []*ast.Field{
							{
								Names: []*ast.Ident{ast.NewIdent("s")},
								Type:  &ast.StarExpr{X: ast.NewIdent(genType.clientImplName())},
							},
						}},
						Name: ast.NewIdent(methField.Names[0].Name),
						Type: meth,
						Body: &ast.BlockStmt{List: body},
					}
					fmt.Fprintln(&w, astString(decl))
					fmt.Fprintln(&w)
				}
			}
		}
	}
	return format.Source(w.Bytes())
}

// qualifyPkgRefs qualifies all refs to non-package-qualified non-builtin types in f so that they refer to definitions in pkg. E.g., 'func(x MyType) -> func (x pkg.MyType)'.
func qualifyPkgRefs(f *ast.FuncType, pkg string) {
	var qualify func(x ast.Expr) ast.Expr
	qualify = func(x ast.Expr) ast.Expr {
		switch y := x.(type) {
		case *ast.Ident:
			if ast.IsExported(y.Name) {
				return &ast.SelectorExpr{X: ast.NewIdent(pkg), Sel: y}
			}
		case *ast.StarExpr:
			y.X = qualify(y.X)
		case *ast.ArrayType:
			y.Elt = qualify(y.Elt)
		case *ast.MapType:
			y.Key = qualify(y.Key)
			y.Value = qualify(y.Value)
		}
		return x
	}
	for _, p := range f.Params.List {
		p.Type = qualify(p.Type)
	}
	for _, r := range f.Results.List {
		r.Type = qualify(r.Type)
	}
}

// synthesizeFieldNamesIfMissing adds synthesized variable names to fl
// if it contains fields with no name. E.g., the field list in
// `func(string, int)` would be converted to `func(v0 string, v1
// int)`.
func synthesizeFieldNamesIfMissing(fl *ast.FieldList) {
	for i, f := range fl.List {
		if len(f.Names) == 0 {
			var name string
			if i == 0 {
				name = "ctx"
			} else {
				name = fmt.Sprintf("v%d", i)
			}
			f.Names = []*ast.Ident{ast.NewIdent(name)}
		}
	}
}

func fieldListToIdentList(fl *ast.FieldList) []ast.Expr {
	var fs []ast.Expr
	for _, f := range fl.List {
		for _, name := range f.Names {
			x := ast.Expr(ast.NewIdent(name.Name))
			fs = append(fs, x)
		}
	}
	return fs
}

func resultType(ft *ast.FuncType) string {
	return astString(ft.Results.List[0].Type.(*ast.StarExpr).X)
}

func hasEllipsis(fl *ast.FieldList) bool {
	if fl.List == nil {
		return false
	}
	_, ok := fl.List[len(fl.List)-1].Type.(*ast.Ellipsis)
	return ok
}

func ellipsisIfNeeded(fl *ast.FieldList) token.Pos {
	if hasEllipsis(fl) {
		return 1
	}
	return 0
}

func astString(x ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, x); err != nil {
		panic(err)
	}
	return buf.String()
}

func astParse(src string) []ast.Stmt {
	x, err := parser.ParseFile(fset, "p.go", "package p; func F() {"+src+"}", 0)
	if err != nil {
		log.Fatalf("AST parse failed: %s.\n\nSource was:\n\n%s", err, src)
	}
	return x.Decls[0].(*ast.FuncDecl).Body.List
}
