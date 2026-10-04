//go:build !js

package fractal

// yield does nothing natively, where the workers run on their own threads.
func yield() {}
