package handlers_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain wraps the package's tests with goleak so any background
// goroutine that escapes the test scope -- most likely a forgotten
// WaitForBackgroundTasks() drain after a click-increment -- fails
// the run with the offending stack trace. The Links handler launches
// a fire-and-forget goroutine on every redirect, so this is the
// package where leak regressions are most likely to land first.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
