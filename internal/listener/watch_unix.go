package listener

import (
	"os"

	"golang.org/x/sys/unix"
)

// readerGone reports whether the read end of the pipe behind f was closed.
func readerGone(f *os.File) bool {
	fds := []unix.PollFd{{Fd: int32(f.Fd())}}
	for {
		_, err := unix.Poll(fds, 0)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return false
		}
		break
	}
	return fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0
}

// ancestors returns the chain of processes above this one that the listener
// watches: its parent, the parent's parent and so on, up to and including
// the first one in another session, at most maxAncestors deep, never init.
//
// A harness typically starts a background command through a wrapper shell in
// a session of its own; the harness itself is then the first ancestor in
// another session. When any link of the chain breaks, the agent is gone.
func ancestors() []int {
	const maxAncestors = 6
	ownSID, err := unix.Getsid(0)
	if err != nil {
		return nil
	}
	var chain []int
	pid := os.Getpid()
	for len(chain) < maxAncestors {
		ppid, err := parentOf(pid)
		if err != nil || ppid <= 1 {
			break
		}
		chain = append(chain, ppid)
		if sid, err := unix.Getsid(ppid); err != nil || sid != ownSID {
			break
		}
		pid = ppid
	}
	return chain
}

// ancestorsIntact reports whether every process of the chain is still the
// parent of the one below it.
func ancestorsIntact(chain []int) bool {
	pid := os.Getpid()
	for _, want := range chain {
		ppid, err := parentOf(pid)
		if err != nil || ppid != want {
			return false
		}
		pid = want
	}
	return true
}
