package state

import (
	"errors"
	"os"
)

// SaveRuntimeState writes the current runtime state atomically to path with 0600 permissions.
func SaveRuntimeState(path string, s RuntimeState) error {
	return AtomicWriteJSON(path, s, 0600)
}

// LoadRuntimeState reads the runtime state from path.
// If the file does not exist, it returns a zero-value RuntimeState, false, nil.
// If an error occurs reading or decoding the file, it returns a zero-value RuntimeState, false, err.
// Otherwise, it returns the loaded state, true, nil.
func LoadRuntimeState(path string) (RuntimeState, bool, error) {
	var s RuntimeState
	if err := ReadJSON(path, &s); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RuntimeState{}, false, nil
		}
		return RuntimeState{}, false, err
	}
	return s, true, nil
}
