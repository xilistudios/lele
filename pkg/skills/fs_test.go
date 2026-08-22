package skills

import "os"

// mkdirAll is a thin wrapper for tests.
func mkdirAll(path string) error {
	return os.MkdirAll(path, 0755)
}

// writeFile is a thin wrapper for tests.
func writeFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
