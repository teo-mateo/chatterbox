package listener

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// parentOf returns the parent process id of pid.
func parentOf(pid int) (int, error) {
	if pid == os.Getpid() {
		return os.Getppid(), nil
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	// pid (comm) state ppid ...; comm may contain spaces and parentheses.
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	fields := strings.Fields(s[i+1:])
	if i < 0 || len(fields) < 2 {
		return 0, fmt.Errorf("unexpected /proc stat format")
	}
	return strconv.Atoi(fields[1])
}
