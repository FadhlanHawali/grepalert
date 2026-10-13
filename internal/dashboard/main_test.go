package dashboard

import (
	"os"
	"testing"
)

// TestMain runs the tests in a temporary directory, so default relative
// paths (like the audit log) never land in the source tree.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "grepalert-dashboard-test")
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(dir); err != nil {
		panic(err)
	}
	code := m.Run()
	os.Chdir(os.TempDir())
	os.RemoveAll(dir)
	os.Exit(code)
}
