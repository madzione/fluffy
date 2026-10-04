package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// insideFlutter is set in the environment of the flutter commands fluffy
// runs, so the build hooks they run can tell (see fromOldHook).
const insideFlutter = "FLUFFY_IN_FLUTTER"

// flutter runs the flutter tool in dir with the terminal attached. Projects
// pinned with FVM (.fvmrc) use "fvm flutter" when fvm is installed.
func flutter(dir string, args ...string) error {
	env := append(os.Environ(), insideFlutter+"=1")
	if _, err := os.Stat(filepath.Join(dir, ".fvmrc")); err == nil {
		if _, err := exec.LookPath("fvm"); err == nil {
			return attached(dir, env, "fvm", append([]string{"flutter"}, args...)...)
		}
	}
	return attached(dir, env, "flutter", args...)
}

func fvm(dir string, args ...string) error {
	return attached(dir, nil, "fvm", args...)
}

// attached runs a command in dir with the terminal attached. A nil env
// inherits fluffy's environment.
func attached(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// fvmVersion returns the Flutter version FVM uses in dir: the one pinned in
// the .fvmrc of dir or a parent, else the global version. It is empty when
// neither is set or fvm cannot be queried.
func fvmVersion(dir string) string {
	var project struct {
		Project struct {
			PinnedVersion *struct{ Name string } `json:"pinnedVersion"`
		} `json:"project"`
	}
	if fvmAPI(dir, &project, "project") == nil && project.Project.PinnedVersion != nil {
		return project.Project.PinnedVersion.Name
	}

	// fvm global has no read-only form, it prompts. The global version is
	// the target of the globalCacheLink symlink.
	var context struct {
		Context struct {
			GlobalCacheLink string `json:"globalCacheLink"`
		} `json:"context"`
	}
	if fvmAPI(dir, &context, "context") != nil || context.Context.GlobalCacheLink == "" {
		return ""
	}
	target, err := os.Readlink(context.Context.GlobalCacheLink)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func fvmAPI(dir string, v any, args ...string) error {
	cmd := exec.Command("fvm", append([]string{"api"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(out, v)
}

// runCommand generates the API, builds the web library when the app may
// start in a browser, then hands over to flutter run. Native targets build
// Go in their hooks.
func runCommand(args []string) error {
	root, err := findProjectRoot()
	if err != nil {
		return err
	}

	if err := generateBefore(root); err != nil {
		return err
	}

	device, hasDevice := deviceArg(args)
	_, webErr := os.Stat(filepath.Join(root, "web"))
	// Without -d flutter picks the device itself, which may be a browser.
	if isWebDevice(device) || (!hasDevice && webErr == nil) {
		if err := buildWeb(root, slices.Contains(args, "--release")); err != nil {
			return err
		}
	}

	return flutter(root, append([]string{"run"}, args...)...)
}

// deviceArg returns the value of -d/--device-id from flutter run arguments.
func deviceArg(args []string) (string, bool) {
	for i, arg := range args {
		switch {
		case arg == "-d" || arg == "--device-id":
			if i+1 < len(args) {
				return args[i+1], true
			}
		case strings.HasPrefix(arg, "--device-id="):
			return strings.TrimPrefix(arg, "--device-id="), true
		case strings.HasPrefix(arg, "-d") && len(arg) > 2:
			return strings.TrimPrefix(arg, "-d"), true
		}
	}
	return "", false
}

func isWebDevice(device string) bool {
	switch strings.ToLower(device) {
	case "chrome", "edge", "web-server":
		return true
	}
	return false
}
