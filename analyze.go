package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// The API between Dart and Go, as fluffy generate finds it in the Go code:
// the channels registered with fluffy.Handle, On, Bind and Export, the
// functions marked with //fluffy:export, the channels Go sends to with
// fluffy.Emit and Request, and the interfaces marked with //fluffy:dart.

type api struct {
	root   string         // Flutter project root
	module string         // module path of golang/
	fluffy string         // import path of the fluffy package
	main   *types.Package // golang/
	fset   *token.FileSet
	docs   map[token.Pos]*ast.CommentGroup // by the position of the declared name
	dirs   []string                        // directories of the scanned packages and their modules

	endpoints []*endpoint // channels Dart sends to
	inbound   []*inbound  // channels Go sends to
	ifaces    []*dartIface
	exports   []*export // registered by the generated Go file

	problems []problem
}

type endpointKind int

const (
	kindHandle endpointKind = iota // fluffy.Handle: data T, answer R
	kindOn                         // fluffy.On: data T, no answer
	kindFunc                       // Bind, Export: positional arguments
)

// endpoint is a channel handled in Go.
type endpoint struct {
	kind     endpointKind
	channel  string
	doc      string
	pos      token.Pos
	arg      types.Type // Handle, On: the data, nil for none
	argParam string     // Handle, On: the parameter name of the Go function
	implicit bool       // a method of a bound type: skipped with a warning if unsupported
	params   []param    // kindFunc
	result   types.Type // nil for none
	stream   bool       // kindFunc returning a channel, result is its element
}

// inbound is a channel Go sends to Dart.
type inbound struct {
	channel string
	request bool       // fluffy.Request, else fluffy.Emit
	data    types.Type // nil for none
	result  types.Type // Request only, nil for none
	pos     token.Pos
	iface   bool // a method of a //fluffy:dart interface
}

type param struct {
	name string
	typ  types.Type
}

// dartIface is an interface marked with //fluffy:dart: Go calls it, Dart
// implements it.
type dartIface struct {
	obj     *types.TypeName
	class   string // Dart class
	prefix  string // channel prefix
	ctor    string // generated Go constructor
	doc     string
	methods []*ifaceMethod
}

type ifaceMethod struct {
	name    string // Go method name
	doc     string
	ctx     bool
	params  []param
	result  types.Type // nil for none
	request bool       // has an error result, else emitted
}

// export is a //fluffy:export function, registered by the generated Go file.
type export struct {
	channel string
	fn      *types.Func
}

type problem struct {
	pos     token.Position
	msg     string
	warning bool
}

func (a *api) errorf(pos token.Pos, format string, args ...any) {
	a.problems = append(a.problems, problem{a.position(pos), fmt.Sprintf(format, args...), false})
}

func (a *api) warnf(pos token.Pos, format string, args ...any) {
	a.problems = append(a.problems, problem{a.position(pos), fmt.Sprintf(format, args...), true})
}

func (a *api) position(pos token.Pos) token.Position {
	p := a.fset.Position(pos)
	if rel, err := filepath.Rel(a.root, p.Filename); err == nil && !strings.HasPrefix(rel, "..") {
		p.Filename = rel
	}
	return p
}

func (p problem) String() string {
	prefix := ""
	if p.warning {
		prefix = "warning: "
	}
	if !p.pos.IsValid() {
		return prefix + p.msg
	}
	return fmt.Sprintf("%s: %s%s", p.pos, prefix, p.msg)
}

// loadEnv type-checks the code like the web build: without cgo, so the
// native transport drops out and no C toolchain is needed.
var loadEnv = []string{"GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0"}

// analyze loads golang/ and the packages it imports from the user's own
// modules and collects the API.
func analyze(root string) (*api, error) {
	dir := filepath.Join(root, "golang")
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return nil, fmt.Errorf("no golang/go.mod in %s, run fluffy init first", root)
	}
	// The generated file is replaced by an empty one, so a stale version
	// that refers to removed functions does not break the analysis.
	overlay := map[string][]byte{filepath.Join(dir, generatedGo): []byte("package main\n")}
	env := append(os.Environ(), loadEnv...)

	// The import graph tells which packages belong to the user.
	graph, err := packages.Load(&packages.Config{
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedImports | packages.NeedDeps | packages.NeedModule,
		Dir:     dir,
		Env:     env,
		Overlay: overlay,
	}, ".")
	if err != nil {
		return nil, err
	}
	if len(graph) != 1 || graph[0].Module == nil {
		return nil, errors.New("golang/ is not a Go module package")
	}
	a := &api{root: root, module: graph[0].Module.Path, fluffy: graph[0].Module.Path + "/fluffy"}

	var patterns []string
	seen := map[string]bool{}
	dirs := map[string]bool{}
	var visit func(*packages.Package)
	visit = func(pkg *packages.Package) {
		if seen[pkg.PkgPath] {
			return
		}
		seen[pkg.PkgPath] = true
		if !ownModule(pkg.Module) {
			return
		}
		dirs[pkg.Module.Dir] = true
		for _, file := range pkg.GoFiles {
			dirs[filepath.Dir(file)] = true
		}
		if pkg.PkgPath != a.fluffy {
			patterns = append(patterns, pkg.PkgPath)
		}
		for _, imp := range pkg.Imports {
			visit(imp)
		}
	}
	visit(graph[0])
	sort.Strings(patterns)
	for dir := range dirs {
		a.dirs = append(a.dirs, dir)
	}
	sort.Strings(a.dirs)

	// Only the user's packages are checked from source, everything else
	// comes from export data.
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedModule,
		Dir:     dir,
		Env:     env,
		Overlay: overlay,
	}, patterns...)
	if err != nil {
		return a, err
	}
	if len(pkgs) > 0 {
		a.fset = pkgs[0].Fset
	}
	// golang/ first, the rest by import path.
	sort.SliceStable(pkgs, func(i, j int) bool { return pkgs[i].PkgPath == a.module && pkgs[j].PkgPath != a.module })

	a.docs = map[token.Pos]*ast.CommentGroup{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			collectDocs(file, a.docs)
		}
	}
	for _, pkg := range pkgs {
		if pkg.PkgPath == a.module {
			a.main = pkg.Types
		}
		a.scan(pkg)
		a.scanIgnored(pkg)
	}
	a.checkConflicts()

	// Type errors that only come from the generated file being left out.
	generated := map[string]bool{}
	for _, iface := range a.ifaces {
		generated[iface.ctor] = true
	}
	var loadErrs []string
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			// go list also compiles the package for its export data. It is
			// checked from source here, so its type errors say the same.
			if e.Kind == packages.ListError && len(pkg.Syntax) > 0 && pkg.TypesInfo != nil {
				continue
			}
			if name, ok := strings.CutPrefix(e.Msg, "undefined: "); ok && pkg.PkgPath == a.module && generated[name] {
				continue
			}
			pos := e.Pos
			if rel, err := filepath.Rel(root, pos); err == nil && pos != "" && !strings.HasPrefix(rel, "..") {
				pos = rel
			}
			loadErrs = append(loadErrs, strings.TrimPrefix(pos+": "+e.Msg, ": "))
		}
	}
	if len(loadErrs) > 0 {
		return a, fmt.Errorf("the Go code does not compile (checked for the web, GOOS=js GOARCH=wasm):\n  %s", strings.Join(loadErrs, "\n  "))
	}
	return a, nil
}

// ownModule reports if the module belongs to the user: the main module, a
// go.work module or a module replaced by a local directory. Packages of
// other modules are only type-checked, never scanned.
func ownModule(m *packages.Module) bool {
	return m != nil && (m.Main || m.Replace != nil && m.Replace.Version == "")
}

// collectDocs indexes the doc comments of declarations by the position of
// their name, which is the position of the types.Object.
func collectDocs(file *ast.File, docs map[token.Pos]*ast.CommentGroup) {
	fields := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			doc := field.Doc
			if doc == nil {
				doc = field.Comment
			}
			for _, name := range field.Names {
				docs[name.Pos()] = doc
			}
		}
	}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			docs[decl.Name.Pos()] = decl.Doc
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				spec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				doc := spec.Doc
				if doc == nil && len(decl.Specs) == 1 {
					doc = decl.Doc
				}
				docs[spec.Name.Pos()] = doc
				switch t := spec.Type.(type) {
				case *ast.StructType:
					fields(t.Fields)
				case *ast.InterfaceType:
					fields(t.Methods)
				}
			}
		}
	}
}

// directive returns the line //fluffy:<name> of the comment group, or nil.
func directive(doc *ast.CommentGroup, name string) *ast.Comment {
	if doc == nil {
		return nil
	}
	for _, c := range doc.List {
		if text, ok := strings.CutPrefix(c.Text, "//fluffy:"+name); ok && strings.TrimSpace(text) == "" {
			return c
		}
	}
	return nil
}

var anyDirective = regexp.MustCompile(`^//fluffy:(\S*)(.*)`)

func (a *api) scan(pkg *packages.Package) {
	info := pkg.TypesInfo
	if info == nil {
		return
	}
	for _, file := range pkg.Syntax {
		// The directives that belong to a declaration, the others are
		// reported below instead of being ignored silently.
		used := map[*ast.Comment]bool{}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				c := directive(decl.Doc, "export")
				if fn, ok := info.Defs[decl.Name].(*types.Func); ok && c != nil {
					used[c] = true
					a.exportFunc(pkg, fn)
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					spec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					c := directive(spec.Doc, "dart")
					if c == nil && len(decl.Specs) == 1 {
						c = directive(decl.Doc, "dart")
					}
					if obj, ok := info.Defs[spec.Name].(*types.TypeName); ok && c != nil {
						used[c] = true
						a.dartInterface(pkg, obj)
					}
				}
			}
		}

		for _, group := range file.Comments {
			for _, c := range group.List {
				m := anyDirective.FindStringSubmatch(c.Text)
				switch {
				case m == nil || used[c]:
				case m[1] != "export" && m[1] != "dart":
					a.warnf(c.Pos(), "unknown directive //fluffy:%s, fluffy knows //fluffy:export and //fluffy:dart", m[1])
				case strings.TrimSpace(m[2]) != "":
					a.warnf(c.Pos(), "//fluffy:%s takes no arguments, fluffy generate ignores it", m[1])
				case m[1] == "export":
					a.warnf(c.Pos(), "//fluffy:export is not attached to a function, fluffy generate ignores it: put it in the doc comment right above the func, without a blank line in between")
				default:
					a.warnf(c.Pos(), "//fluffy:dart is not attached to a type, fluffy generate ignores it: put it in the doc comment right above the type, without a blank line in between")
				}
			}
		}

		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				a.call(pkg, file, call)
			}
			return true
		})
	}
}

var fluffyUse = regexp.MustCompile(`//fluffy:(export|dart)\b|\bfluffy\.(Handle|On|Emit|Request|Bind|Export)\b`)

// scanIgnored warns about the files of pkg that the web build leaves out
// (e.g. //go:build !js) but that use the fluffy API: generate only sees the
// web build, so their channels are missing from the generated code.
func (a *api) scanIgnored(pkg *packages.Package) {
	for _, name := range pkg.IgnoredFiles {
		if strings.HasSuffix(name, "_test.go") || filepath.Base(name) == generatedGo {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		loc := fluffyUse.FindIndex(src)
		if loc == nil {
			continue
		}
		pos := token.Position{Filename: name, Line: 1 + bytes.Count(src[:loc[0]], []byte("\n"))}
		if rel, err := filepath.Rel(a.root, name); err == nil && !strings.HasPrefix(rel, "..") {
			pos.Filename = rel
		}
		a.problems = append(a.problems, problem{pos, fmt.Sprintf("%s is not part of the web build (build constraints), "+
			"and fluffy generate only reads the web build (GOOS=js GOARCH=wasm): %s here is left out of the generated API",
			filepath.Base(name), src[loc[0]:loc[1]]), true})
	}
}

// call records a call of the fluffy API.
func (a *api) call(pkg *packages.Package, file *ast.File, call *ast.CallExpr) {
	info := pkg.TypesInfo
	fun := ast.Unparen(call.Fun)
	switch f := fun.(type) {
	case *ast.IndexExpr:
		fun = f.X
	case *ast.IndexListExpr:
		fun = f.X
	}
	var id *ast.Ident
	switch f := fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return
	}
	fn, ok := info.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != a.fluffy || fn.Signature().Recv() != nil {
		return
	}
	var typeArgs []types.Type
	if inst, ok := info.Instances[id]; ok {
		for i := 0; i < inst.TypeArgs.Len(); i++ {
			typeArgs = append(typeArgs, inst.TypeArgs.At(i))
		}
	}

	// The position of the channel, the number of arguments and type
	// arguments. Calls that do not match fail to compile and are reported
	// as type errors.
	shapes := map[string][3]int{"Handle": {0, 2, 2}, "On": {0, 2, 1}, "Emit": {0, 2, 0}, "Request": {1, 3, 1}, "Bind": {0, 2, 1}, "Export": {0, 2, 0}}
	shape, ok := shapes[fn.Name()]
	if !ok || len(call.Args) != shape[1] || len(typeArgs) < shape[2] {
		return
	}
	channelArg := shape[0]
	value := info.Types[call.Args[channelArg]].Value
	if value == nil || value.Kind() != constant.String {
		what := "channel"
		if fn.Name() == "Bind" {
			what = "namespace"
		}
		a.warnf(call.Pos(), "fluffy.%s with a %s that is not a constant, fluffy generate skips it", fn.Name(), what)
		return
	}
	channel := constant.StringVal(value)
	if channel == "" {
		a.errorf(call.Pos(), "fluffy.%s with an empty channel", fn.Name())
		return
	}

	switch fn.Name() {
	case "Handle":
		a.endpoints = append(a.endpoints, &endpoint{kind: kindHandle, channel: channel, pos: call.Pos(),
			doc: a.docAbove(file, call), arg: dataType(typeArgs[0]), argParam: firstParam(info.TypeOf(call.Args[1])),
			result: dataType(typeArgs[1])})
	case "On":
		a.endpoints = append(a.endpoints, &endpoint{kind: kindOn, channel: channel, pos: call.Pos(),
			doc: a.docAbove(file, call), arg: dataType(typeArgs[0]), argParam: firstParam(info.TypeOf(call.Args[1]))})
	case "Emit":
		a.inbound = append(a.inbound, &inbound{channel: channel, pos: call.Pos(), data: dataType(info.TypeOf(call.Args[1]))})
	case "Request":
		a.inbound = append(a.inbound, &inbound{channel: channel, request: true, pos: call.Pos(),
			data: dataType(info.TypeOf(call.Args[2])), result: dataType(typeArgs[0])})
	case "Export":
		t := info.TypeOf(call.Args[1])
		if t == nil {
			return
		}
		sig, ok := t.Underlying().(*types.Signature)
		if !ok {
			a.errorf(call.Pos(), "fluffy.Export %q: not a function", channel)
			return
		}
		ep, err := funcEndpoint(sig)
		if err != nil {
			a.errorf(call.Pos(), "fluffy.Export %q: %v", channel, err)
			return
		}
		ep.channel, ep.pos, ep.doc = channel, call.Pos(), a.docAbove(file, call)
		a.endpoints = append(a.endpoints, ep)
	case "Bind":
		a.bind(channel, typeArgs[0], call.Pos())
	}
}

// bind records the methods fluffy.Bind registers, with the same rules.
func (a *api) bind(namespace string, t types.Type, pos token.Pos) {
	methods := types.NewMethodSet(t)
	for i := 0; i < methods.Len(); i++ {
		fn := methods.At(i).Obj().(*types.Func)
		if !fn.Exported() {
			continue
		}
		ep, err := funcEndpoint(fn.Signature())
		if err != nil {
			a.warnf(pos, "fluffy.Bind %q skips %s: %v", namespace, fn.Name(), err)
			continue
		}
		ep.channel, ep.pos, ep.doc, ep.implicit = namespace+"."+fn.Name(), pos, a.doc(fn.Pos()), true
		a.endpoints = append(a.endpoints, ep)
	}
	if methods.Len() == 0 {
		a.warnf(pos, "fluffy.Bind %q: %s has no methods", namespace, types.TypeString(t, nil))
	}
}

func (a *api) exportFunc(pkg *packages.Package, fn *types.Func) {
	sig := fn.Signature()
	switch {
	case sig.Recv() != nil:
		a.errorf(fn.Pos(), "//fluffy:export on the method %s, bind its type with fluffy.Bind instead", fn.Name())
		return
	case sig.TypeParams().Len() > 0:
		a.errorf(fn.Pos(), "//fluffy:export on the generic function %s", fn.Name())
		return
	case pkg.PkgPath != a.module && !fn.Exported():
		a.errorf(fn.Pos(), "//fluffy:export on %s, which is not exported and cannot be registered from golang/", fn.Name())
		return
	case pkg.PkgPath != a.module && pkg.Name == "main":
		a.errorf(fn.Pos(), "//fluffy:export on %s in a main package outside golang/, which cannot be imported", fn.Name())
		return
	}
	ep, err := funcEndpoint(sig)
	if err != nil {
		a.errorf(fn.Pos(), "//fluffy:export %s: %v", fn.Name(), err)
		return
	}
	channel := fn.Name()
	if pkg.PkgPath != a.module {
		channel = pkg.Name + "." + fn.Name()
	}
	ep.channel, ep.pos, ep.doc = channel, fn.Pos(), a.doc(fn.Pos())
	a.endpoints = append(a.endpoints, ep)
	a.exports = append(a.exports, &export{channel, fn})
}

func (a *api) dartInterface(pkg *packages.Package, obj *types.TypeName) {
	iface, ok := obj.Type().Underlying().(*types.Interface)
	switch {
	case !ok:
		a.errorf(obj.Pos(), "//fluffy:dart on %s, which is not an interface", obj.Name())
		return
	case isGeneric(obj):
		a.errorf(obj.Pos(), "//fluffy:dart on the generic interface %s", obj.Name())
		return
	case pkg.PkgPath != a.module && !obj.Exported():
		a.errorf(obj.Pos(), "//fluffy:dart on %s, which is not exported and cannot be implemented in golang/", obj.Name())
		return
	case !iface.IsMethodSet():
		a.errorf(obj.Pos(), "//fluffy:dart on %s, which is a type constraint", obj.Name())
		return
	}

	d := &dartIface{obj: obj, class: upperCamel(obj.Name()), prefix: obj.Name(), doc: a.doc(obj.Pos())}
	if pkg.PkgPath != a.module {
		d.class = upperCamel(pkg.Name) + upperCamel(obj.Name())
		d.prefix = pkg.Name + "." + obj.Name()
	}
	d.ctor = "new" + d.class

	for i := 0; i < iface.NumMethods(); i++ {
		fn := iface.Method(i)
		if !fn.Exported() {
			a.errorf(fn.Pos(), "//fluffy:dart interface %s has the unexported method %s", obj.Name(), fn.Name())
			continue
		}
		sig := fn.Signature()
		m := &ifaceMethod{name: fn.Name(), doc: a.doc(fn.Pos())}
		var err error
		m.ctx, m.params, err = funcParams(sig)
		switch res := sig.Results(); {
		case err != nil:
		case res.Len() == 0:
		case res.Len() == 1 && isError(res.At(0).Type()):
			m.request = true
		case res.Len() == 2 && isError(res.At(1).Type()) && !isError(res.At(0).Type()):
			m.request, m.result = true, dataType(res.At(0).Type())
		default:
			err = errors.New("results must be nothing (emit), error or (R, error) (request)")
		}
		if err != nil {
			a.errorf(fn.Pos(), "//fluffy:dart %s.%s: %v", obj.Name(), fn.Name(), err)
			continue
		}
		d.methods = append(d.methods, m)

		in := &inbound{channel: d.prefix + "." + m.name, request: m.request, result: m.result, pos: fn.Pos(), iface: true}
		a.inbound = append(a.inbound, in)
	}
	a.ifaces = append(a.ifaces, d)
}

// isGeneric reports if a declared type or alias has type parameters. An
// alias of an instance (type UI = Callbacks[string]) is not generic.
func isGeneric(obj *types.TypeName) bool {
	switch t := obj.Type().(type) {
	case *types.Named:
		return t.TypeParams().Len() > 0
	case *types.Alias:
		return t.TypeParams().Len() > 0
	}
	return false
}

// firstParam returns the name of the first parameter of a function type.
func firstParam(t types.Type) string {
	if t == nil {
		return ""
	}
	if sig, ok := t.Underlying().(*types.Signature); ok && sig.Params().Len() > 0 {
		return sig.Params().At(0).Name()
	}
	return ""
}

// funcEndpoint checks a signature against the rules of fluffy.Bind.
func funcEndpoint(sig *types.Signature) (*endpoint, error) {
	if sig.TypeParams().Len() > 0 {
		return nil, errors.New("generic functions are not supported")
	}
	_, params, err := funcParams(sig)
	if err != nil {
		return nil, err
	}
	ep := &endpoint{kind: kindFunc, params: params}
	res := sig.Results()
	switch {
	case res.Len() == 0:
	case res.Len() == 1 && isError(res.At(0).Type()):
	case res.Len() == 1, res.Len() == 2 && isError(res.At(1).Type()) && !isError(res.At(0).Type()):
		ep.result = res.At(0).Type()
		if ch, ok := ep.result.Underlying().(*types.Chan); ok && ch.Dir() != types.SendOnly {
			ep.result, ep.stream = ch.Elem(), true
		}
		ep.result = dataType(ep.result)
	default:
		return nil, errors.New("results must be nothing, R, error or (R, error)")
	}
	return ep, nil
}

// funcParams returns the parameters after an optional context.Context.
func funcParams(sig *types.Signature) (ctx bool, params []param, err error) {
	if sig.Variadic() {
		return false, nil, errors.New("variadic functions are not supported")
	}
	for i := 0; i < sig.Params().Len(); i++ {
		p := sig.Params().At(i)
		if i == 0 && isContext(p.Type()) {
			ctx = true
			continue
		}
		name := p.Name()
		if name == "" || name == "_" {
			name = fmt.Sprintf("arg%d", len(params)+1)
		}
		params = append(params, param{name, p.Type()})
	}
	return ctx, params, nil
}

func isContext(t types.Type) bool {
	return types.TypeString(t, nil) == "context.Context"
}

func isError(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}

// dataType returns nil for types that carry no data: struct{} and untyped
// nil.
func dataType(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	if b, ok := t.(*types.Basic); ok && b.Kind() == types.UntypedNil {
		return nil
	}
	if s, ok := types.Unalias(t).(*types.Struct); ok && s.NumFields() == 0 {
		return nil
	}
	return t
}

// docAbove returns the comment on the lines right above a call statement.
func (a *api) docAbove(file *ast.File, call *ast.CallExpr) string {
	line := a.fset.Position(call.Pos()).Line
	for _, group := range file.Comments {
		if a.fset.Position(group.End()).Line == line-1 {
			return commentText(group)
		}
	}
	return ""
}

// doc returns the doc comment of the object declared at pos.
func (a *api) doc(pos token.Pos) string {
	return commentText(a.docs[pos])
}

func commentText(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	return strings.TrimSpace(group.Text())
}

// checkConflicts reports what the runtime would accept silently: channels
// registered twice and channels used with different types.
func (a *api) checkConflicts() {
	handled := map[string]*endpoint{}
	var endpoints []*endpoint
	for _, ep := range a.endpoints {
		if first, ok := handled[ep.channel]; ok {
			a.errorf(ep.pos, "channel %q is registered twice, first at %s (the second registration replaces the first)", ep.channel, a.position(first.pos))
			continue
		}
		handled[ep.channel] = ep
		endpoints = append(endpoints, ep)
	}
	a.endpoints = endpoints

	sent := map[string]*inbound{}
	var inbound []*inbound
	for _, in := range a.inbound {
		first, ok := sent[in.channel]
		switch {
		case !ok:
			sent[in.channel] = in
			inbound = append(inbound, in)
		case first.iface || in.iface:
			a.errorf(in.pos, "channel %q is also used by %s", in.channel, a.position(first.pos))
		case first.request != in.request:
			a.errorf(in.pos, "channel %q is used with both fluffy.Emit and fluffy.Request, first at %s", in.channel, a.position(first.pos))
		case !sameType(first.data, in.data) || !sameType(first.result, in.result):
			a.errorf(in.pos, "channel %q is sent with %s here and with %s at %s", in.channel,
				typeString(in.data), typeString(first.data), a.position(first.pos))
		}
	}
	a.inbound = inbound

	for _, in := range a.inbound {
		ep, ok := handled[in.channel]
		if !ok {
			continue
		}
		if in.request || ep.kind == kindFunc || !sameType(in.data, ep.arg) {
			a.errorf(in.pos, "channel %q is sent from Go and handled in Go at %s with different types", in.channel, a.position(ep.pos))
		}
	}
}

func sameType(x, y types.Type) bool {
	if x == nil || y == nil {
		return x == nil && y == nil
	}
	return types.Identical(x, y)
}

func typeString(t types.Type) string {
	if t == nil {
		return "no data"
	}
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}
