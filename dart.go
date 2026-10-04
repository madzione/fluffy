package main

import (
	"fmt"
	"go/token"
	"go/types"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/tools/go/types/typeutil"
)

// Generation of lib/golang/api.g.dart: a typed Dart function per channel,
// a class per Bind namespace, an abstract class per //fluffy:dart interface
// and a class per Go struct that is sent.

// dartType is how a Go type looks in Dart and how it is converted from and
// to the decoded JSON. e is always a primary expression (an identifier,
// member access or index), so it can be used as a receiver.
type dartType struct {
	name    string                       // Dart type
	cast    bool                         // decode is "e as name"
	decode  func(e string, d int) string // JSON value -> name
	encode  func(e string, d int) string // name -> JSON value, nil when it is one already
	nonNull *dartType                    // the type without ?, set by nullable
}

func castType(name string) *dartType {
	return &dartType{name: name, cast: true, decode: func(e string, _ int) string {
		if strings.HasPrefix(e, "await ") {
			e = "(" + e + ")" // not needed, but clearer
		}
		return e + " as " + name
	}}
}

var objectType = &dartType{name: "Object?", decode: func(e string, _ int) string { return e }}

func (t *dartType) enc(e string, d int) string {
	if t.encode == nil {
		return e
	}
	return t.encode(e, d)
}

func nullable(t *dartType) *dartType {
	if strings.HasSuffix(t.name, "?") {
		return t
	}
	if t.cast {
		n := castType(t.name + "?")
		n.nonNull = t
		return n
	}
	n := &dartType{name: t.name + "?", nonNull: t}
	n.decode = func(e string, d int) string {
		v := fmt.Sprintf("v%d", d)
		return fmt.Sprintf("switch %s { final %s? => %s, _ => null }", parens(e), v, t.decode(v, d+1))
	}
	if t.encode != nil {
		n.encode = func(e string, d int) string {
			v := fmt.Sprintf("v%d", d)
			return fmt.Sprintf("switch %s { final %s? => %s, _ => null }", parens(e), v, t.encode(v, d+1))
		}
	}
	return n
}

// parens wraps e in parentheses unless it already is.
func parens(e string) string {
	if strings.HasPrefix(e, "(") {
		depth := 0
		for i, r := range e {
			switch r {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				if i == len(e)-1 {
					return e
				}
				break
			}
		}
	}
	return "(" + e + ")"
}

type dartClass struct {
	name   string
	doc    string
	fields []dartField
	err    error
	addr   bool // the fields were mapped as addressable, see mapType
}

type dartField struct {
	name string // Dart name
	key  string // JSON key
	doc  string
	typ  *dartType
	// omit is set for optional fields (omitempty, omitzero or behind an
	// embedded pointer): the type of the value, which toJson leaves out
	// when it is null. Go would allocate the embedded pointer for a null.
	omit *dartType
}

// dartGen maps Go types to Dart and keeps the generated classes.
type dartGen struct {
	api     *api
	classes []*dartClass
	byType  typeutil.Map // *types.Named -> *dartClass
	names   map[string]string
	imports map[string]bool
}

// Names the generated code uses or that golang.dart exports next to it.
var dartTaken = []string{
	"bool", "int", "double", "num", "String", "List", "Map", "Set", "Object", "Null", "Never", "Function",
	"Iterable", "Future", "FutureOr", "Stream", "StreamSubscription", "StreamController", "Completer",
	"Duration", "DateTime", "Uint8List", "MapEntry", "Record", "Type", "Symbol", "Enum", "Error",
	"Exception", "Comparable", "Pattern", "RegExp", "Uri", "BigInt", "Sink", "Timer", "Zone",
	"base64Decode", "base64Encode", "jsonDecode", "jsonEncode", "print", "identical", "main",
	"emitter", "receiver", "Bridge", "Emitter", "Receiver", "GolangException", "Handler", "StreamHandler",
}

// The lower-case names in dartTaken that the generated code refers to inside
// classes and functions: a member or parameter with one of these names would
// shadow it, e.g. a field int makes "as int" fail to compile.
var dartShadowed = []string{"bool", "int", "double", "num", "emitter", "receiver",
	"base64Decode", "base64Encode", "jsonDecode", "jsonEncode"}

// Members every Dart object has.
var objectMembers = []string{"hashCode", "runtimeType", "toString", "noSuchMethod"}

var dartReserved = map[string]bool{}

func init() {
	for _, word := range strings.Fields(`abstract as assert async await base break case catch class const
		continue covariant default deferred do dynamic else enum export extends extension external factory
		false final finally for Function get hide if implements import in interface is late library mixin
		new null of on operator part required rethrow return sealed set show static super switch sync this
		throw true try type typedef var void when while with yield`) {
		dartReserved[word] = true
	}
}

// dartIdent makes name a valid Dart identifier that is none of avoid.
func dartIdent(name string, avoid ...string) string {
	for dartReserved[name] || slices.Contains(avoid, name) {
		name += "_"
	}
	return name
}

// words splits a Go or channel name at characters Dart identifiers cannot
// have.
func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// lowerCamel turns Go and channel names into Dart member names: Add -> add,
// UserID -> userID, HTTPServer -> httpServer, CPUs -> cpus, hello_world ->
// helloWorld.
func lowerCamel(s string) string {
	parts := words(s)
	if len(parts) == 0 {
		return "value"
	}
	first := []rune(parts[0])
	upper := 0
	for upper < len(first) && unicode.IsUpper(first[upper]) {
		upper++
	}
	if upper > 1 && upper < len(first) {
		// The last capital starts the next word (HTTPServer), unless an s
		// makes the initialism plural (CPUs, URLsByHost).
		rest := first[upper:]
		if rest[0] != 's' || len(rest) > 1 && !unicode.IsUpper(rest[1]) {
			upper--
		}
	}
	for i := 0; i < upper; i++ {
		first[i] = unicode.ToLower(first[i])
	}
	if upper == 0 && len(first) > 0 {
		first[0] = unicode.ToLower(first[0])
	}
	out := string(first)
	for _, part := range parts[1:] {
		out += capitalize(part)
	}
	if unicode.IsDigit([]rune(out)[0]) {
		out = "n" + out
	}
	return out
}

// upperCamel turns Go and channel names into Dart class names.
func upperCamel(s string) string {
	out := ""
	for _, part := range words(s) {
		out += capitalize(part)
	}
	if out == "" || unicode.IsDigit([]rune(out)[0]) {
		out = "Go" + out
	}
	return out
}

func capitalize(s string) string {
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func dartString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, `$`, `\$`, "\n", `\n`, "\r", `\r`)
	return "'" + r.Replace(s) + "'"
}

func dartDoc(doc, indent string) string {
	if doc == "" {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		b.WriteString(strings.TrimRight(indent+"/// "+line, " ") + "\n")
	}
	return b.String()
}

// mapType returns the Dart type of a Go type that is sent as JSON, following
// encoding/json. addr tells if Go has the value addressable when it encodes
// it: encoding/json calls marshal methods with a pointer receiver only on
// addressable values, which are the ones behind a pointer, slice elements and
// their fields. Values Go returns or sends as data are not, values Go decodes
// are.
func (g *dartGen) mapType(t types.Type, addr bool) (*dartType, error) {
	t = types.Unalias(t)
	if named, ok := t.(*types.Named); ok {
		switch typeKey(named) {
		case "time.Time":
			return &dartType{
				name:   "DateTime",
				decode: func(e string, _ int) string { return "DateTime.parse(" + e + " as String)" },
				encode: func(e string, _ int) string { return e + ".toUtc().toIso8601String()" },
			}, nil
		case "time.Duration":
			return &dartType{
				name:   "Duration",
				decode: func(e string, _ int) string { return "Duration(microseconds: (" + e + " as int) ~/ 1000)" },
				encode: func(e string, _ int) string { return e + ".inMicroseconds * 1000" },
			}, nil
		}
		if hasMethod(t, "MarshalJSON", addr) {
			return objectType, nil
		}
		if hasMethod(t, "MarshalText", addr) {
			return castType("String"), nil
		}
		if st, ok := named.Underlying().(*types.Struct); ok {
			return g.class(named, st, addr)
		}
	}

	switch u := t.Underlying().(type) {
	case *types.Basic:
		info := u.Info()
		switch {
		case info&types.IsBoolean != 0:
			return castType("bool"), nil
		case info&types.IsInteger != 0:
			return castType("int"), nil
		case info&types.IsFloat != 0:
			return &dartType{name: "double", decode: func(e string, _ int) string { return "(" + e + " as num).toDouble()" }}, nil
		case info&types.IsString != 0:
			return castType("String"), nil
		}
		return nil, fmt.Errorf("%s cannot be sent as JSON", u)
	case *types.Pointer:
		elem, err := g.mapType(u.Elem(), true)
		if err != nil {
			return nil, err
		}
		return nullable(elem), nil
	case *types.Slice:
		if isByte(u.Elem()) {
			g.imports["dart:convert"], g.imports["dart:typed_data"] = true, true
			return &dartType{
				name: "Uint8List",
				decode: func(e string, d int) string {
					v := fmt.Sprintf("v%d", d)
					return fmt.Sprintf("switch %s { final String %s => base64Decode(%s), _ => Uint8List(0) }", parens(e), v, v)
				},
				encode: func(e string, _ int) string { return "base64Encode(" + e + ")" },
			}, nil
		}
		return g.listType(u.Elem(), true)
	case *types.Array:
		return g.listType(u.Elem(), addr)
	case *types.Map:
		return g.mapOf(u)
	case *types.Struct:
		if u.NumFields() > 0 {
			return castType("Map<String, dynamic>"), nil
		}
		return objectType, nil
	case *types.Interface:
		return objectType, nil
	}
	return nil, fmt.Errorf("%s cannot be sent as JSON", types.TypeString(t, shortQualifier))
}

func (g *dartGen) listType(elemType types.Type, addr bool) (*dartType, error) {
	elem, err := g.mapType(elemType, addr)
	if err != nil {
		return nil, err
	}
	name := "List<" + elem.name + ">"
	t := &dartType{name: name}
	t.decode = func(e string, d int) string {
		x := fmt.Sprintf("x%d", d)
		return fmt.Sprintf("(%s as List<dynamic>?)?.map((%s) => %s).toList() ?? const <%s>[]", e, x, elem.decode(x, d+1), elem.name)
	}
	if elem.encode != nil {
		t.encode = func(e string, d int) string {
			x := fmt.Sprintf("x%d", d)
			return fmt.Sprintf("[for (final %s in %s) %s]", x, e, elem.encode(x, d+1))
		}
	}
	return t, nil
}

// mapOf maps a Go map. Its values and keys are never addressable.
func (g *dartGen) mapOf(m *types.Map) (*dartType, error) {
	elem, err := g.mapType(m.Elem(), false)
	if err != nil {
		return nil, err
	}
	key := m.Key()
	intKey := false
	switch b, _ := key.Underlying().(*types.Basic); {
	case b != nil && b.Info()&types.IsString != 0, hasMethod(key, "MarshalText", false):
	case b != nil && b.Info()&types.IsInteger != 0:
		intKey = true
	default:
		return nil, fmt.Errorf("%s cannot be sent as JSON, map keys have to be strings or integers", types.TypeString(m, shortQualifier))
	}

	keyName := "String"
	if intKey {
		keyName = "int"
	}
	t := &dartType{name: "Map<" + keyName + ", " + elem.name + ">"}
	t.decode = func(e string, d int) string {
		k, x := fmt.Sprintf("k%d", d), fmt.Sprintf("x%d", d)
		mapKey := k
		if intKey {
			mapKey = "int.parse(" + k + ")"
		}
		return fmt.Sprintf("(%s as Map<String, dynamic>?)?.map((%s, %s) => MapEntry(%s, %s)) ?? const <%s, %s>{}",
			e, k, x, mapKey, elem.decode(x, d+1), keyName, elem.name)
	}
	if elem.encode != nil || intKey {
		t.encode = func(e string, d int) string {
			x := fmt.Sprintf("x%d", d)
			k := x + ".key"
			if intKey {
				k = "'${" + x + ".key}'"
			}
			return fmt.Sprintf("{for (final %s in %s.entries) %s: %s}", x, e, k, elem.enc(x+".value", d+1))
		}
	}
	return t, nil
}

// isByte reports if a slice of t is sent as base64. encoding/json checks the
// methods of *t for that.
func isByte(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Kind() == types.Uint8 && !marshals(t, true)
}

// hasMethod reports if encoding/json finds the method (of Marshaler or
// TextMarshaler) on t: only with a value receiver, or also with a pointer
// receiver when the value is addressable.
func hasMethod(t types.Type, name string, addr bool) bool {
	if _, ok := t.Underlying().(*types.Interface); ok {
		return false
	}
	if addr {
		t = types.NewPointer(t)
	}
	return types.NewMethodSet(t).Lookup(nil, name) != nil
}

// marshals reports if encoding/json encodes t with its own method.
func marshals(t types.Type, addr bool) bool {
	return hasMethod(t, "MarshalJSON", addr) || hasMethod(t, "MarshalText", addr)
}

// pointerMarshaler returns "T.Method" for a marshal method with a pointer
// receiver that makes encoding/json encode t differently when it is
// addressable, in t or in the fields it inlines. It is empty when t is
// encoded the same either way.
func pointerMarshaler(t types.Type, seen map[types.Type]bool) string {
	t = types.Unalias(t)
	if seen[t] {
		return ""
	}
	seen[t] = true
	if named, ok := t.(*types.Named); ok {
		for _, method := range []string{"MarshalJSON", "MarshalText"} {
			switch {
			case hasMethod(t, method, false):
				return ""
			case hasMethod(t, method, true):
				return named.Obj().Name() + "." + method
			}
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		for _, f := range jsonFields(u) {
			if m := pointerMarshaler(f.v.Type(), seen); m != "" {
				return m
			}
		}
	case *types.Array:
		return pointerMarshaler(u.Elem(), seen)
	}
	return ""
}

func typeKey(named *types.Named) string {
	if named.Obj().Pkg() == nil {
		return named.Obj().Name()
	}
	return named.Obj().Pkg().Path() + "." + named.Obj().Name()
}

func shortQualifier(p *types.Package) string { return p.Name() }

// class returns the Dart class of a Go struct type. A struct type gets one
// class, however often it is used, so it has to be encoded the same whether
// it is addressable or not (see mapType).
func (g *dartGen) class(named *types.Named, st *types.Struct, addr bool) (*dartType, error) {
	c, _ := g.byType.At(named).(*dartClass)
	if c == nil {
		c = &dartClass{name: g.className(named), doc: g.api.doc(named.Obj().Pos()), addr: addr}
		g.byType.Set(named, c)
		g.classes = append(g.classes, c)
		c.err = g.fields(c, st, addr)
		if c.err != nil {
			c.err = fmt.Errorf("%s: %w", types.TypeString(named, shortQualifier), c.err)
		}
	}
	if c.err != nil {
		return nil, c.err
	}
	if c.addr != addr {
		if m := pointerMarshaler(st, map[types.Type]bool{}); m != "" {
			return nil, fmt.Errorf("%s is sent both as a value and through a pointer (or in a slice), which encoding/json encodes differently "+
				"because %s has a pointer receiver: give it a value receiver", types.TypeString(named, shortQualifier), m)
		}
	}
	return &dartType{
		name:   c.name,
		decode: func(e string, _ int) string { return c.name + ".fromJson(" + e + ")" },
		encode: func(e string, _ int) string { return e + ".toJson()" },
	}, nil
}

// className uses the Go type name, prefixed with the package name when it
// is taken. Type arguments are part of the name: Page[Item] is PageItem.
func (g *dartGen) className(named *types.Named) string {
	base := upperCamel(named.Obj().Name())
	if args := named.TypeArgs(); args != nil {
		for i := 0; i < args.Len(); i++ {
			base += upperCamel(types.TypeString(args.At(i), shortQualifier))
		}
	}
	prefix := "Go"
	if pkg := named.Obj().Pkg(); pkg != nil && pkg.Path() != g.api.module {
		prefix = upperCamel(pkg.Name())
	}
	name := base
	for i := 1; g.names[name] != "" || dartReserved[name]; i++ {
		name = prefix + base
		if i > 1 {
			name = fmt.Sprintf("%s%s%d", prefix, base, i)
		}
	}
	g.names[name] = "the class for " + types.TypeString(named, shortQualifier)
	return name
}

type jsonField struct {
	v        *types.Var
	key      string
	tagged   bool
	index    []int
	optional bool // omitempty, omitzero or behind an embedded pointer
	quoted   bool // the ,string option
}

// jsonFields returns the fields encoding/json sends for a struct, with its
// rules for tags and embedded structs.
func jsonFields(st *types.Struct) []jsonField {
	var all []jsonField
	var walk func(st *types.Struct, index []int, optional bool, seen map[*types.Struct]bool)
	walk = func(st *types.Struct, index []int, optional bool, seen map[*types.Struct]bool) {
		if seen[st] {
			return
		}
		seen[st] = true
		defer delete(seen, st)
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			tag := reflect.StructTag(st.Tag(i)).Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			idx := append(slices.Clone(index), i)
			if f.Embedded() && name == "" {
				t, ptr := types.Unalias(f.Type()), false
				if p, ok := t.Underlying().(*types.Pointer); ok {
					t, ptr = p.Elem(), true
				}
				if est, ok := t.Underlying().(*types.Struct); ok {
					walk(est, idx, optional || ptr, seen)
					continue
				}
				if !f.Exported() {
					continue
				}
			} else if !f.Exported() {
				continue
			}
			field := jsonField{v: f, key: name, tagged: name != "", index: idx, optional: optional}
			if field.key == "" {
				field.key = f.Name()
			}
			for _, opt := range strings.Split(opts, ",") {
				switch opt {
				case "omitempty":
					// Structs and non-empty arrays are never empty, only
					// omitzero leaves them out.
					field.optional = field.optional || !neverEmpty(f.Type())
				case "omitzero":
					field.optional = true
				case "string":
					// Also behind one unnamed pointer, a nil pointer stays
					// null.
					t := types.Unalias(f.Type())
					if p, ok := t.(*types.Pointer); ok {
						t = p.Elem()
					}
					if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&(types.IsBoolean|types.IsNumeric|types.IsString) != 0 {
						field.quoted = true
					}
				}
			}
			all = append(all, field)
		}
	}
	walk(st, nil, false, map[*types.Struct]bool{})

	byKey := map[string][]jsonField{}
	for _, f := range all {
		byKey[f.key] = append(byKey[f.key], f)
	}
	var fields []jsonField
	for _, group := range byKey {
		depth := len(group[0].index)
		for _, f := range group {
			depth = min(depth, len(f.index))
		}
		var top, tagged []jsonField
		for _, f := range group {
			if len(f.index) == depth {
				top = append(top, f)
				if f.tagged {
					tagged = append(tagged, f)
				}
			}
		}
		switch {
		case len(top) == 1:
			fields = append(fields, top[0])
		case len(tagged) == 1:
			fields = append(fields, tagged[0])
		}
	}
	sort.Slice(fields, func(i, j int) bool { return slices.Compare(fields[i].index, fields[j].index) < 0 })
	return fields
}

// neverEmpty reports if omitempty never leaves a value of t out.
func neverEmpty(t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Struct:
		return true
	case *types.Array:
		return u.Len() > 0
	}
	return false
}

func (g *dartGen) fields(c *dartClass, st *types.Struct, addr bool) error {
	used := append(slices.Clone(objectMembers), dartShadowed...)
	used = append(used, "toJson", "fromJson", c.name)
	for _, f := range jsonFields(st) {
		t, err := g.mapType(f.v.Type(), addr)
		if err != nil {
			return fmt.Errorf("field %s: %w", f.v.Name(), err)
		}
		// ,string quotes the value behind a pointer, unless its type
		// marshals itself.
		elem, elemAddr, ptr := types.Unalias(f.v.Type()), addr, false
		if p, ok := elem.(*types.Pointer); ok {
			elem, elemAddr, ptr = p.Elem(), true, true
		}
		if f.quoted && !marshals(elem, elemAddr) {
			g.imports["dart:convert"] = true
			inner := t
			if ptr {
				inner = t.nonNull
			}
			t = &dartType{
				name:   inner.name,
				decode: func(e string, d int) string { return inner.decode("jsonDecode("+e+" as String)", d) },
				encode: func(e string, d int) string { return "jsonEncode(" + inner.enc(e, d) + ")" },
			}
			if ptr {
				t = nullable(t)
			}
		}
		var omit *dartType
		if f.optional {
			omit = t
			if t.nonNull != nil {
				omit = t.nonNull // a pointer: toJson only encodes it when not null
			}
			t = nullable(t)
		}
		name := dartIdent(lowerCamel(f.v.Name()), used...)
		used = append(used, name)
		c.fields = append(c.fields, dartField{name: name, key: f.key, doc: g.api.doc(f.v.Pos()), typ: t, omit: omit})
	}
	return nil
}

// A function, stream or handler in the generated API.
type dartMember struct {
	name string
	pos  token.Pos
	code string
}

// dartScope is the top level or the class of a namespace.
type dartScope struct {
	namespace string
	members   []*dartMember
	names     map[string]token.Pos
}

// splitChannel splits "todo.Add" into the namespace "todo" and the member
// "Add".
func splitChannel(channel string) (namespace, member string) {
	if i := strings.LastIndex(channel, "."); i > 0 && i < len(channel)-1 {
		return channel[:i], channel[i+1:]
	}
	return "", channel
}

// generateDart writes the Dart side of the API.
func (a *api) generateDart() []byte {
	g := &dartGen{api: a, names: map[string]string{}, imports: map[string]bool{}}
	for _, name := range dartTaken {
		g.names[name] = "used by the generated code"
	}

	var scopes []*dartScope
	scopeOf := map[string]*dartScope{}
	scope := func(namespace string) *dartScope {
		s := scopeOf[namespace]
		if s == nil {
			s = &dartScope{namespace: namespace, names: map[string]token.Pos{}}
			scopeOf[namespace] = s
			scopes = append(scopes, s)
		}
		return s
	}
	scope("")
	// skip is set for the methods of bound types: they are skipped with a
	// warning when their types cannot be sent, like unsupported signatures.
	add := func(s *dartScope, name string, pos token.Pos, code func(name string) (string, error), skip string) {
		avoid := dartShadowed
		if s.namespace != "" {
			avoid = append(slices.Clone(objectMembers), dartShadowed...)
		}
		name = dartIdent(name, avoid...)
		if first, ok := s.names[name]; ok {
			a.errorf(pos, "the Dart name %s is already used for %s", qualified(s, name), a.position(first))
			return
		}
		src, err := code(name)
		if err != nil {
			if skip != "" {
				a.warnf(pos, "%s: %v", skip, err)
			} else {
				a.errorf(pos, "%v", err)
			}
			return
		}
		s.names[name] = pos
		s.members = append(s.members, &dartMember{name, pos, src})
	}

	handled := map[string]bool{}
	for _, ep := range a.endpoints {
		handled[ep.channel] = true
	}
	for _, ep := range a.endpoints {
		ns, member := splitChannel(ep.channel)
		ep := ep
		skip := ""
		if ep.implicit {
			skip = fmt.Sprintf("fluffy.Bind %q skips %s", ns, member)
		}
		add(scope(ns), lowerCamel(member), ep.pos, func(name string) (string, error) { return g.endpoint(ep, name) }, skip)
	}
	for _, in := range a.inbound {
		if in.iface {
			continue
		}
		ns, member := splitChannel(in.channel)
		name := lowerCamel(member)
		switch {
		case in.request:
			name = "handle" + upperCamel(member)
		case handled[in.channel]:
			name = "on" + upperCamel(member)
		}
		in := in
		add(scope(ns), name, in.pos, func(name string) (string, error) { return g.inbound(in, name) }, "")
	}

	// Top level names: functions, namespace getters and classes, interfaces.
	top := scopeOf[""]
	topName := func(name string, pos token.Pos, what string) bool {
		if first, ok := top.names[name]; ok {
			a.errorf(pos, "the Dart name %s for %s is already used for %s", name, what, a.position(first))
			return false
		}
		if g.names[name] != "" {
			a.errorf(pos, "the Dart name %s for %s is %s", name, what, g.names[name])
			return false
		}
		top.names[name] = pos
		g.names[name] = what
		return true
	}
	for name, pos := range top.names {
		if g.names[name] != "" {
			a.errorf(pos, "the Dart name %s is %s", name, g.names[name])
		}
		g.names[name] = "a generated function"
	}
	var nsCode []string
	for _, s := range scopes[1:] {
		if len(s.members) == 0 {
			continue
		}
		getter, class := lowerCamel(s.namespace), "Go"+upperCamel(s.namespace)
		pos := s.members[0].pos
		if !topName(getter, pos, fmt.Sprintf("the namespace %q", s.namespace)) || !topName(class, pos, fmt.Sprintf("the namespace %q", s.namespace)) {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "/// The Go channels %s.*: a bound Go type (fluffy.Bind) or the exported functions of a package.\nconst %s = %s._();\n\n", s.namespace, getter, class)
		fmt.Fprintf(&b, "/// The Go channels %s.*, use them through [%s].\nclass %s {\n  const %s._();\n", s.namespace, getter, class, class)
		for _, m := range s.members {
			b.WriteString("\n" + indent(m.code, "  "))
		}
		b.WriteString("}\n")
		nsCode = append(nsCode, b.String())
	}
	var ifaceCode []string
	for _, iface := range a.ifaces {
		if !topName(iface.class, iface.obj.Pos(), "the interface "+iface.obj.Name()) {
			continue
		}
		code, err := g.iface(iface)
		if err != nil {
			a.errorf(iface.obj.Pos(), "//fluffy:dart %s: %v", iface.obj.Name(), err)
			continue
		}
		ifaceCode = append(ifaceCode, code)
	}

	var body strings.Builder
	for _, m := range top.members {
		body.WriteString("\n" + m.code)
	}
	for _, code := range nsCode {
		body.WriteString("\n" + code)
	}
	for _, code := range ifaceCode {
		body.WriteString("\n" + code)
	}
	for _, c := range g.classes {
		if c.err == nil {
			body.WriteString("\n" + g.classCode(c))
		}
	}

	var out strings.Builder
	out.WriteString(`// Generated by fluffy generate from the Go code in golang/. Do not edit, run
// fluffy generate again after changing the Go side.

// ignore_for_file: type=lint

`)
	imports := []string{}
	for lib := range g.imports {
		imports = append(imports, lib)
	}
	sort.Strings(imports)
	for _, lib := range imports {
		fmt.Fprintf(&out, "import '%s';\n", lib)
	}
	if len(imports) > 0 {
		out.WriteString("\n")
	}
	out.WriteString("import 'src/bridge.dart';\n")
	out.WriteString(body.String())
	return []byte(out.String())
}

func qualified(s *dartScope, name string) string {
	if s.namespace == "" {
		return name
	}
	return lowerCamel(s.namespace) + "." + name
}

func indent(code, prefix string) string {
	lines := strings.Split(strings.TrimSuffix(code, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// params maps Go parameters to Dart ones. addr is false when Go sends the
// arguments, see mapType.
func (g *dartGen) params(params []param, addr bool) ([]string, []*dartType, error) {
	var names []string
	var types []*dartType
	for _, p := range params {
		t, err := g.mapType(p.typ, addr)
		if err != nil {
			return nil, nil, fmt.Errorf("parameter %s: %w", p.name, err)
		}
		names = append(names, dartIdent(lowerCamel(p.name), append(slices.Clone(dartShadowed), names...)...))
		types = append(types, t)
	}
	return names, types, nil
}

// result maps a Go result, nil (no result) is void. addr is true when Go
// decodes the result, see mapType.
func (g *dartGen) result(t types.Type, addr bool) (*dartType, error) {
	if t == nil {
		return nil, nil
	}
	r, err := g.mapType(t, addr)
	if err != nil {
		return nil, fmt.Errorf("result: %w", err)
	}
	return r, nil
}

// endpoint writes the Dart function that calls a channel handled in Go.
func (g *dartGen) endpoint(ep *endpoint, name string) (string, error) {
	var b strings.Builder
	b.WriteString(dartDoc(ep.doc, ""))
	channel := dartString(ep.channel)

	if ep.kind == kindFunc {
		names, types, err := g.params(ep.params, true)
		if err != nil {
			return "", err
		}
		result, err := g.result(ep.result, false)
		if err != nil {
			return "", err
		}
		var decl, args []string
		for i, n := range names {
			decl = append(decl, types[i].name+" "+n)
			args = append(args, types[i].enc(n, 0))
		}
		call := fmt.Sprintf("%s, [%s]", channel, strings.Join(args, ", "))
		switch {
		case ep.stream && (result == nil || result == objectType):
			fmt.Fprintf(&b, "Stream<Object?> %s(%s) =>\n    emitter.subscribe(%s);\n", name, strings.Join(decl, ", "), call)
		case ep.stream:
			fmt.Fprintf(&b, "Stream<%s> %s(%s) =>\n    emitter.subscribe(%s).map((x0) => %s);\n", result.name, name, strings.Join(decl, ", "), call, result.decode("x0", 1))
		case result == nil:
			fmt.Fprintf(&b, "Future<void> %s(%s) =>\n    emitter.request<Object?>(%s);\n", name, strings.Join(decl, ", "), call)
		default:
			fmt.Fprintf(&b, "Future<%s> %s(%s) async =>\n    %s;\n", result.name, name, strings.Join(decl, ", "), result.decode("await emitter.request<Object?>("+call+")", 0))
		}
		return b.String(), nil
	}

	var decl, data string
	if ep.arg != nil {
		t, err := g.mapType(ep.arg, true)
		if err != nil {
			return "", fmt.Errorf("data: %w", err)
		}
		n := dartIdent(lowerCamel(ep.argName()), dartShadowed...)
		decl, data = t.name+" "+n, ", "+t.enc(n, 0)
	}
	if ep.kind == kindOn {
		fmt.Fprintf(&b, "void %s(%s) => emitter.emit(%s%s);\n", name, decl, channel, data)
		return b.String(), nil
	}
	result, err := g.result(ep.result, false)
	if err != nil {
		return "", err
	}
	if result == nil {
		fmt.Fprintf(&b, "Future<void> %s(%s) =>\n    emitter.request<Object?>(%s%s);\n", name, decl, channel, data)
	} else {
		fmt.Fprintf(&b, "Future<%s> %s(%s) async =>\n    %s;\n", result.name, name, decl, result.decode("await emitter.request<Object?>("+channel+data+")", 0))
	}
	return b.String(), nil
}

// argName is the parameter name of the Go handler, if it has one.
func (ep *endpoint) argName() string {
	if ep.argParam != "" && ep.argParam != "_" {
		return ep.argParam
	}
	return "data"
}

// inbound writes the Dart side of a channel Go sends to.
func (g *dartGen) inbound(in *inbound, name string) (string, error) {
	var data *dartType
	if in.data != nil {
		var err error
		if data, err = g.mapType(in.data, false); err != nil {
			return "", fmt.Errorf("data: %w", err)
		}
	}
	channel := dartString(in.channel)
	var b strings.Builder

	if !in.request {
		fmt.Fprintf(&b, "/// The data Go emits on %s (fluffy.Emit).\n", channel)
		switch {
		case data == nil:
			fmt.Fprintf(&b, "Stream<void> %s() => receiver.on<Object?>(%s);\n", name, channel)
		case data == objectType:
			fmt.Fprintf(&b, "Stream<Object?> %s() => receiver.on<Object?>(%s);\n", name, channel)
		default:
			fmt.Fprintf(&b, "Stream<%s> %s() =>\n    receiver.on<Object?>(%s).map((x0) => %s);\n", data.name, name, channel, data.decode("x0", 1))
		}
		return b.String(), nil
	}

	result, err := g.result(in.result, true)
	if err != nil {
		return "", err
	}
	g.imports["dart:async"] = true
	fn, arg := "FutureOr<void> Function(", "handler()"
	if data != nil {
		fn += data.name + " data"
		arg = "handler(" + data.decode("data", 0) + ")"
	}
	fn += ")"
	if result != nil {
		fn = "FutureOr<" + result.name + "> Function(" + strings.TrimPrefix(fn, "FutureOr<void> Function(")
	}
	fmt.Fprintf(&b, "/// Answers the requests Go sends on %s (fluffy.Request) with [handler].\n", channel)
	fmt.Fprintf(&b, "void %s(%s handler) {\n  receiver.handle(%s, (data) async {\n", name, fn, channel)
	switch {
	case result == nil:
		fmt.Fprintf(&b, "    await %s;\n    return null;\n", arg)
	case result.encode == nil:
		fmt.Fprintf(&b, "    return await %s;\n", arg)
	default:
		fmt.Fprintf(&b, "    final result = await %s;\n    return %s;\n", arg, result.enc("result", 0))
	}
	b.WriteString("  });\n}\n")
	return b.String(), nil
}

// iface writes the abstract class of a //fluffy:dart interface.
func (g *dartGen) iface(d *dartIface) (string, error) {
	var b, register strings.Builder
	doc := d.doc
	if doc != "" {
		doc += "\n\n"
	}
	goName := d.obj.Pkg().Name() + "." + d.obj.Name()
	if d.obj.Pkg().Path() == g.api.module {
		goName = d.obj.Name() + " in golang/"
	}
	doc += fmt.Sprintf("Go calls Dart through the interface %s (marked with //fluffy:dart). Implement this class and pass it to [register].", goName)
	b.WriteString(dartDoc(doc, ""))
	fmt.Fprintf(&b, "abstract class %s {\n", d.class)

	used := append(slices.Clone(objectMembers), "register")
	emits := false
	for _, m := range d.methods {
		name := dartIdent(lowerCamel(m.name), dartShadowed...)
		if slices.Contains(used, name) {
			return "", fmt.Errorf("the method %s is %s in Dart, which is taken", m.name, name)
		}
		used = append(used, name)

		names, types, err := g.params(m.params, false)
		if err != nil {
			return "", fmt.Errorf("%s: %w", m.name, err)
		}
		result, err := g.result(m.result, true)
		if err != nil {
			return "", fmt.Errorf("%s: %w", m.name, err)
		}
		var decl, args []string
		for i, n := range names {
			decl = append(decl, types[i].name+" "+n)
			args = append(args, types[i].decode(fmt.Sprintf("args[%d]", i), 0))
		}
		ret := "void"
		switch {
		case m.request && result != nil:
			ret = "FutureOr<" + result.name + ">"
		case m.request:
			ret = "FutureOr<void>"
		}
		if len(used) > len(objectMembers)+2 {
			b.WriteString("\n")
		}
		b.WriteString(dartDoc(m.doc, "  "))
		fmt.Fprintf(&b, "  %s %s(%s);\n", ret, name, strings.Join(decl, ", "))

		channel := dartString(d.prefix + "." + m.name)
		call := fmt.Sprintf("impl.%s(%s)", name, strings.Join(args, ", "))
		argsLine := ""
		if len(names) > 0 {
			argsLine = "      final args = data as List<dynamic>;\n"
		}
		if !m.request {
			emits = true
			fmt.Fprintf(&register, "    _subscriptions.add(receiver.on<Object?>(%s).listen((data) {\n%s      %s;\n    }));\n", channel, argsLine, call)
			continue
		}
		fmt.Fprintf(&register, "    receiver.handle(%s, (data) async {\n%s", channel, argsLine)
		switch {
		case result == nil:
			fmt.Fprintf(&register, "      await %s;\n      return null;\n", call)
		case result.encode == nil:
			fmt.Fprintf(&register, "      return await %s;\n", call)
		default:
			fmt.Fprintf(&register, "      final result = await %s;\n      return %s;\n", call, result.enc("result", 0))
		}
		register.WriteString("    });\n")
	}
	g.imports["dart:async"] = true

	fmt.Fprintf(&b, "\n  /// Makes [impl] answer the calls of Go. Registering again replaces it.\n  static void register(%s impl) {\n", d.class)
	if emits {
		b.WriteString("    for (final subscription in _subscriptions) {\n      subscription.cancel();\n    }\n    _subscriptions.clear();\n")
	}
	b.WriteString(register.String())
	b.WriteString("  }\n")
	if emits {
		b.WriteString("\n  static final _subscriptions = <StreamSubscription<Object?>>[];\n")
	}
	b.WriteString("}\n")
	return b.String(), nil
}

func (g *dartGen) classCode(c *dartClass) string {
	var b strings.Builder
	b.WriteString(dartDoc(c.doc, ""))
	fmt.Fprintf(&b, "class %s {\n", c.name)
	if len(c.fields) == 0 {
		fmt.Fprintf(&b, "  const %s();\n\n  factory %s.fromJson(Object? json) => const %s();\n\n", c.name, c.name, c.name)
		fmt.Fprintf(&b, "  Map<String, Object?> toJson() => {};\n\n  @override\n  String toString() => '%s()';\n}\n", c.name)
		return b.String()
	}

	fmt.Fprintf(&b, "  const %s({\n", c.name)
	for _, f := range c.fields {
		if strings.HasSuffix(f.typ.name, "?") {
			fmt.Fprintf(&b, "    this.%s,\n", f.name)
		} else {
			fmt.Fprintf(&b, "    required this.%s,\n", f.name)
		}
	}
	b.WriteString("  });\n\n")

	fmt.Fprintf(&b, "  factory %s.fromJson(Object? json) {\n    final m = json as Map<String, dynamic>;\n    return %s(\n", c.name, c.name)
	for _, f := range c.fields {
		fmt.Fprintf(&b, "      %s: %s,\n", f.name, f.typ.decode("m["+dartString(f.key)+"]", 0))
	}
	b.WriteString("    );\n  }\n")

	for _, f := range c.fields {
		b.WriteString("\n" + dartDoc(f.doc, "  "))
		fmt.Fprintf(&b, "  final %s %s;\n", f.typ.name, f.name)
	}

	b.WriteString("\n  Map<String, Object?> toJson() => {\n")
	for _, f := range c.fields {
		if f.omit != nil {
			fmt.Fprintf(&b, "    if (%s case final v0?) %s: %s,\n", f.name, dartString(f.key), f.omit.enc("v0", 1))
		} else {
			fmt.Fprintf(&b, "    %s: %s,\n", dartString(f.key), f.typ.enc(f.name, 0))
		}
	}
	b.WriteString("  };\n\n  @override\n")
	var parts []string
	for _, f := range c.fields {
		parts = append(parts, f.name+": $"+f.name)
	}
	fmt.Fprintf(&b, "  String toString() => '%s(%s)';\n}\n", c.name, strings.Join(parts, ", "))
	return b.String()
}
