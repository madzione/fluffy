# Go ↔ Dart type mapping

| Go | Dart |
|---|---|
| `int*`, `uint*` | `int` (web: precision lost above 2^53) |
| `float*` | `double` |
| `string`, `bool` | `String`, `bool` |
| `[]byte` | `Uint8List` (raw bytes on the wire as an argument, result, stream value or a struct field of one; base64 deeper down) |
| `[]T`, `[N]T` | `List<T>` (nil arrives as empty list) |
| `map[string]T`, `map[int]T` | `Map<String,T>`, `Map<int,T>` (nil arrives as empty map) |
| `*T`, fields with `omitempty` (non-struct) or `omitzero` | `T?` |
| struct | generated class with `fromJson`/`toJson`, follows `json` tags like `encoding/json` |
| `time.Time` | `DateTime` (microseconds; zone becomes UTC) |
| `time.Duration` | `Duration` (microseconds) |
| type with `MarshalText` | `String` |
| `any`, interfaces, type with `MarshalJSON` | `Object?` |
| `<-chan T` result | `Stream<T>` |
| `error` result | throws `GolangException` |
| channels as data, funcs, complex | unsupported: `fluffy generate` fails |

Give `MarshalText`/`MarshalJSON` value receivers: with a pointer receiver `encoding/json` treats values and pointers differently and generation can fail. Same-named structs from two packages: the second Dart class gets a package-name prefix.
