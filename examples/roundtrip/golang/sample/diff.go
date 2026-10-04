package sample

import (
	"fmt"
	"reflect"
	"time"
)

// Diff lists the fields where got differs from want, empty when they are
// equal. JSON cannot tell a nil slice or map from an empty one, so those
// count as equal, and times are compared as instants: the zone is not sent
// to Dart, which only knows UTC and local time.
func Diff(got, want Everything) []string {
	var diffs []string
	diff(&diffs, "", reflect.ValueOf(got), reflect.ValueOf(want))
	return diffs
}

var timeType = reflect.TypeOf(time.Time{})

func diff(diffs *[]string, path string, got, want reflect.Value) {
	report := func() {
		*diffs = append(*diffs, fmt.Sprintf("%s: got %s, want %s", path, show(got), show(want)))
	}
	if got.IsValid() != want.IsValid() {
		report()
		return
	}
	if !got.IsValid() {
		return
	}
	if got.Type() != want.Type() {
		report()
		return
	}
	if got.Type() == timeType {
		if !got.Interface().(time.Time).Equal(want.Interface().(time.Time)) {
			report()
		}
		return
	}

	switch got.Kind() {
	case reflect.Struct:
		for i := 0; i < got.NumField(); i++ {
			if got.Type().Field(i).IsExported() {
				diff(diffs, join(path, got.Type().Field(i).Name), got.Field(i), want.Field(i))
			}
		}
	case reflect.Pointer, reflect.Interface:
		if got.IsNil() || want.IsNil() {
			if got.IsNil() != want.IsNil() {
				report()
			}
			return
		}
		diff(diffs, path, got.Elem(), want.Elem())
	case reflect.Slice, reflect.Array:
		if got.Len() != want.Len() {
			report()
			return
		}
		for i := 0; i < got.Len(); i++ {
			diff(diffs, fmt.Sprintf("%s[%d]", path, i), got.Index(i), want.Index(i))
		}
	case reflect.Map:
		if got.Len() != want.Len() {
			report()
			return
		}
		for _, key := range want.MapKeys() {
			diff(diffs, fmt.Sprintf("%s[%v]", path, key), got.MapIndex(key), want.MapIndex(key))
		}
	default:
		if !reflect.DeepEqual(got.Interface(), want.Interface()) {
			report()
		}
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func show(v reflect.Value) string {
	if !v.IsValid() {
		return "nothing"
	}
	if v.Type() == timeType {
		return v.Interface().(time.Time).Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%#v", v.Interface())
}
