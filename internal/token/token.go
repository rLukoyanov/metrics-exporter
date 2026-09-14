package token

import "os"

// Read returns the contents of the Kubernetes service account token file,
// or an empty string when the file does not exist or the path is empty.
func Read(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// Present reports whether the token file exists at path.
func Present(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
