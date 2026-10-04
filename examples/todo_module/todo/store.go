//go:build !js

package todo

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// FileStore keeps the items in a JSON file.
type FileStore struct {
	Path string
}

// DefaultStore keeps the items in todos.json in the user's config directory.
func DefaultStore() (Store, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	return FileStore{filepath.Join(dir, "fluffy-todo", "todos.json")}, nil
}

// configDir is os.UserConfigDir, except on Android, which doesn't set $HOME
// for apps: there it is the app's files directory, found through the package
// name, the first argument of the app process.
func configDir() (string, error) {
	if runtime.GOOS != "android" {
		return os.UserConfigDir()
	}
	cmdline, err := os.ReadFile("/proc/self/cmdline")
	if err != nil {
		return "", err
	}
	name, _, _ := bytes.Cut(cmdline, []byte{0})
	name, _, _ = bytes.Cut(name, []byte(":")) // e.g. dev.fluffy.todo:service
	if len(name) == 0 {
		return "", errors.New("no package name in /proc/self/cmdline")
	}
	return filepath.Join("/data/data", string(name), "files"), nil
}

// Load returns no items when the file doesn't exist yet.
func (f FileStore) Load() ([]Item, error) {
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []Item
	return items, json.Unmarshal(data, &items)
}

// Save replaces the file, so a crash never leaves half of it behind.
func (f FileStore) Save(items []Item) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}
