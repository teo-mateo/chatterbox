package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// RuntimeDirEnv overrides the runtime directory.
const RuntimeDirEnv = "CHATTERBOX_RUNTIME_DIR"

// NickEnv names the listener a command talks to.
const NickEnv = "CHATTERBOX_NICK"

// RuntimeDir returns the directory holding control sockets and lock files.
// It deliberately ignores XDG_RUNTIME_DIR: a listener and a command started
// with different environments must still look in the same place.
func RuntimeDir() string {
	if d := os.Getenv(RuntimeDirEnv); d != "" {
		return d
	}
	return fmt.Sprintf("/tmp/chatterbox-%d", os.Getuid())
}

// EnsureRuntimeDir creates dir with mode 0700, or checks that an existing one
// is a real directory owned by the current user with mode 0700.
func EnsureRuntimeDir(dir string) error {
	return ensureRuntimeDir(dir, os.Getuid())
}

func ensureRuntimeDir(dir string, uid int) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("cannot create runtime directory %s: %v", dir, err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("cannot inspect runtime directory %s: %v", dir, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("runtime directory %s is a symbolic link; refusing to use it", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("runtime directory %s is not a directory", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
		return fmt.Errorf("runtime directory %s is owned by uid %d, not by the current user; refusing to use it", dir, st.Uid)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		return fmt.Errorf("runtime directory %s has mode %04o, expected 0700; refusing to use it", dir, perm)
	}
	return nil
}

func socketName(nick string) string { return strings.ToLower(nick) }

// SocketPath is the control socket of the listener for nick.
func SocketPath(dir, nick string) string { return filepath.Join(dir, socketName(nick)+".sock") }

// LockPath is the lock file of the listener for nick. It is never deleted.
func LockPath(dir, nick string) string { return filepath.Join(dir, socketName(nick)+".lock") }

// maxSocketPath is the usable length of sun_path, without the trailing NUL.
func maxSocketPath() int {
	if runtime.GOOS == "darwin" {
		return 103
	}
	return 107
}

// CheckSocketPath fails when path does not fit the platform's socket address.
func CheckSocketPath(path string) error {
	if len(path) > maxSocketPath() {
		return fmt.Errorf("socket path %s is %d bytes, longer than the platform limit of %d; set %s to a shorter directory",
			path, len(path), maxSocketPath(), RuntimeDirEnv)
	}
	return nil
}

// CommandClass says how a command may find its listener (D15).
type CommandClass int

const (
	// ClassListen is the listener itself: the nickname must be explicit.
	ClassListen CommandClass = iota
	// ClassPosting commands act under an identity and never guess it.
	ClassPosting
	// ClassReadOnly commands may auto-detect a single running listener.
	ClassReadOnly
)

// ExplicitNick returns the nickname given by flag or environment, flag first.
func ExplicitNick(flag string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv(NickEnv)
}

// MayAutoDetect reports whether a command of this class may pick the only
// running listener when no nickname was given.
func (c CommandClass) MayAutoDetect() bool { return c == ClassReadOnly }
