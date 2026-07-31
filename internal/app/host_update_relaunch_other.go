//go:build !darwin

package app

import "errors"

func startUpdateRelaunchHelper(_ string) error {
	return errors.New("update relaunch helper is only available on macOS")
}

// RunUpdateRelaunchHelper recognizes the internal helper invocation. Non-macOS
// builds never consume it, so normal CLI arguments keep their existing meaning.
func RunUpdateRelaunchHelper(_ []string) (bool, error) {
	return false, nil
}
