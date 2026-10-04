package todo

import (
	"encoding/json"
	"syscall/js"
)

// localStorage keeps the items in the browser's localStorage.
type localStorage struct{ key string }

// DefaultStore keeps the items in the browser's localStorage.
func DefaultStore() (Store, error) {
	return localStorage{"fluffy-todo"}, nil
}

func (l localStorage) Load() ([]Item, error) {
	value := js.Global().Get("localStorage").Call("getItem", l.key)
	if value.IsNull() {
		return nil, nil
	}
	var items []Item
	return items, json.Unmarshal([]byte(value.String()), &items)
}

func (l localStorage) Save(items []Item) error {
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	js.Global().Get("localStorage").Call("setItem", l.key, string(data))
	return nil
}
