//go:build !windows || !cgo

package hotkey

// NewGlobal сообщает, что global hook недоступен для текущей платформы.
func NewGlobal(key string) (Hook, error) {
	if _, err := normalizeKey(key); err != nil {
		return nil, err
	}
	return nil, ErrUnavailable
}
