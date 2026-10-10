package listener

import (
	"os"

	"golang.org/x/sys/unix"
)

// parentOf returns the parent process id of pid.
func parentOf(pid int) (int, error) {
	if pid == os.Getpid() {
		return os.Getppid(), nil
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	return int(kp.Eproc.Ppid), nil
}
