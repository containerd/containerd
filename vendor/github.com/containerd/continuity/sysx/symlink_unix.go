// +build !windows

package sysx

func evalSymlinks(path string) (string, error) {
	return walkSymlinks(path)
}
