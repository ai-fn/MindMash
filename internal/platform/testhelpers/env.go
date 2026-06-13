// Package testhelpers provides shared test utility functions for the platform packages.
package testhelpers

import (
	"os"
	"testing"
)

// UnsetAll temporarily unsets the given env vars and restores them after t completes.
func UnsetAll(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		k := k
		prev, existed := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("setup: unset %s: %v", k, err)
		}
		t.Cleanup(func() {
			if existed {
				if err := os.Setenv(k, prev); err != nil {
					t.Errorf("cleanup: restore %s: %v", k, err)
				}
			} else {
				if err := os.Unsetenv(k); err != nil {
					t.Errorf("cleanup: unset %s: %v", k, err)
				}
			}
		})
	}
}
