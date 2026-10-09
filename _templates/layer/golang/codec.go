package fluffy

import (
	"bytes"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unicode"
)

// Byte slices are the one type that JSON makes expensive: base64 on both
// sides and a long string to scan. A value that is a byte slice, or a byte
// slice field of a struct value, is sent as a blob next to the JSON instead,
// and the receiver puts it back where encoding/json would have put it. The
// JSON then has null there, or nothing for omitempty. Everything else,
// including byte slices deeper down, stays base64 as encoding/json writes
// it.

// encodeData encodes v as JSON and its byte slices as blobs. A panic of a
// MarshalJSON or MarshalText method becomes an error.
func encodeData(v any) (data json.RawMessage, blobs []blob, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = panicError(r)
		}
	}()

	rv := reflect.ValueOf(v)
	switch {
	case !rv.IsValid():
	case isBytes(rv.Type()):
		if rv.Len() > 0 {
			return nil, []blob{{data: rv.Bytes()}}, nil
		}
	case rv.Kind() == reflect.Struct:
		return encodeStruct(rv, false)
	case rv.Kind() == reflect.Pointer && !rv.IsNil() && rv.Elem().Kind() == reflect.Struct:
		return encodeStruct(rv.Elem(), true)
	}
	data, err = json.Marshal(v)
	return data, nil, err
}

// encodeStruct encodes the struct s with its byte slice fields as blobs.
// pointer tells if s was sent behind a pointer: encoding/json then finds
// marshal methods with pointer receivers on the fields, so the copy is
// encoded through a pointer too.
func encodeStruct(s reflect.Value, pointer bool) (json.RawMessage, []blob, error) {
	info := structOf(s.Type())
	var blobs []blob
	var cp reflect.Value
	for _, f := range info.bytes {
		field := s.Field(f.index)
		if field.Kind() != reflect.Slice || field.Len() == 0 {
			continue
		}
		if !cp.IsValid() {
			cp = reflect.New(s.Type())
			cp.Elem().Set(s)
		}
		blobs = append(blobs, blob{path: []any{f.name}, data: field.Bytes()})
		cp.Elem().Field(f.index).SetZero()
	}
	v := s.Interface()
	switch {
	case !cp.IsValid():
		if pointer {
			v = s.Addr().Interface()
		}
	case pointer:
		v = cp.Interface()
	default:
		v = cp.Elem().Interface()
	}
	data, err := json.Marshal(v)
	return data, blobs, err
}

// decode is json.Unmarshal that treats missing data as null and puts the
// blobs back into v.
func decode(data json.RawMessage, blobs []blob, v any) error {
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	if len(blobs) == 0 {
		return json.Unmarshal(data, v)
	}
	target := reflect.ValueOf(v).Elem()
	if !placeable(target.Type(), blobs) {
		// Rare: a blob where encoding/json has to decode it. Put it back
		// as base64.
		spliced, err := splice(data, blobs)
		if err != nil {
			return err
		}
		return json.Unmarshal(spliced, v)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	for _, b := range blobs {
		place(target, b)
	}
	return nil
}

// placeable reports if every blob is a byte slice of t itself or one of its
// fields, which place can set directly.
func placeable(t reflect.Type, blobs []blob) bool {
	t = deref(t)
	for _, b := range blobs {
		switch len(b.path) {
		case 0:
			if !isBytes(t) {
				return false
			}
		case 1:
			key, ok := b.path[0].(string)
			if !ok || t.Kind() != reflect.Struct {
				return false
			}
			if _, ok := structOf(t).byName[key]; !ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// place sets the byte slice of v that b belongs to, allocating pointers on
// the way.
func place(v reflect.Value, b blob) {
	v = alloc(v)
	if len(b.path) == 1 {
		v = alloc(v.Field(structOf(v.Type()).byName[b.path[0].(string)]))
	}
	v.Set(reflect.ValueOf(b.data).Convert(v.Type()))
}

func alloc(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	return v
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// splice puts the blobs into the JSON as base64 strings.
func splice(data json.RawMessage, blobs []blob) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	for _, b := range blobs {
		var err error
		if tree, err = setPath(tree, b.path, base64.StdEncoding.EncodeToString(b.data)); err != nil {
			return nil, err
		}
	}
	return json.Marshal(tree)
}

func setPath(tree any, path []any, value any) (any, error) {
	if len(path) == 0 {
		return value, nil
	}
	switch node := tree.(type) {
	case map[string]any:
		key, ok := path[0].(string)
		if !ok {
			return nil, fmt.Errorf("blob path %v: %v is not a key", path, path[0])
		}
		child, err := setPath(node[key], path[1:], value)
		node[key] = child
		return node, err
	case []any:
		i, ok := index(path[0])
		if !ok || i >= len(node) {
			return nil, fmt.Errorf("blob path %v: no index %v", path, path[0])
		}
		child, err := setPath(node[i], path[1:], value)
		node[i] = child
		return node, err
	}
	return nil, fmt.Errorf("blob path %v does not exist", path)
}

// index returns a path element as an index: JSON numbers are float64.
func index(p any) (int, bool) {
	switch i := p.(type) {
	case int:
		return i, i >= 0
	case float64:
		return int(i), i >= 0 && i == float64(int(i))
	}
	return 0, false
}

// splitArgs gives each positional argument the blobs that belong to it.
func splitArgs(blobs []blob, n int) ([][]blob, error) {
	if len(blobs) == 0 {
		return nil, nil
	}
	args := make([][]blob, n)
	for _, b := range blobs {
		if len(b.path) == 0 {
			return nil, errors.New("invalid arguments, want a JSON array: got bytes")
		}
		i, ok := index(b.path[0])
		if !ok || i >= n {
			return nil, fmt.Errorf("invalid arguments: bytes for argument %v", b.path[0])
		}
		args[i] = append(args[i], blob{path: b.path[1:], data: b.data})
	}
	return args, nil
}

var (
	marshalerType       = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType   = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	unmarshalerType     = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// hasMethods reports if t or *t has a method that makes encoding/json treat
// it differently.
func hasMethods(t reflect.Type) bool {
	for _, t := range []reflect.Type{t, reflect.PointerTo(t)} {
		if t.Implements(marshalerType) || t.Implements(textMarshalerType) ||
			t.Implements(unmarshalerType) || t.Implements(textUnmarshalerType) {
			return true
		}
	}
	return false
}

// isBytes reports if encoding/json encodes and decodes t as base64.
func isBytes(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 &&
		!hasMethods(t) && !hasMethods(t.Elem())
}

type structInfo struct {
	bytes  []bytesField
	byName map[string]int // JSON name -> field index of the byte slices
}

type bytesField struct {
	index int
	name  string
}

var structs sync.Map // reflect.Type -> *structInfo

// structOf returns the byte slice fields of the struct type t. To match
// encoding/json without its rules for embedded fields and name conflicts,
// only structs without embedded fields, without fields of the same name and
// without marshal methods have any.
func structOf(t reflect.Type) *structInfo {
	if info, ok := structs.Load(t); ok {
		return info.(*structInfo)
	}
	info := &structInfo{byName: map[string]int{}}
	names := map[string]bool{}
	simple := !hasMethods(t)
	for i := 0; simple && i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			simple = false
			break
		}
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if !validTag(name) {
			name = f.Name
		}
		if names[name] {
			simple = false
			break
		}
		names[name] = true
		if isBytes(deref(f.Type)) && !hasMethods(f.Type) {
			info.bytes = append(info.bytes, bytesField{i, name})
			info.byName[name] = i
		}
	}
	if !simple {
		info = &structInfo{}
	}
	actual, _ := structs.LoadOrStore(t, info)
	return actual.(*structInfo)
}

// validTag is isValidTag of encoding/json: other names are ignored.
func validTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

func panicError(r any) error {
	if err, ok := r.(error); ok {
		return fmt.Errorf("panic: %w", err)
	}
	return fmt.Errorf("panic: %v", r)
}
