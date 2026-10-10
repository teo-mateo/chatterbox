// Command ergo-dev runs a local Ergo for manual work: `make ergo-up`,
// `make ergo-down`. The server uses Ergo's shipped history and idle
// settings, a self-signed certificate, and port 16697.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/teo-mateo/chatterbox/test/ergo"
)

const port = 16697

func pidFile() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("chatterbox-ergo-dev-%d.pid", os.Getuid()))
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: ergo-dev up|down|serve")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "up":
		if pid, ok := running(); ok {
			fmt.Printf("a local Ergo is already running (pid %d) on 127.0.0.1:%d\n", pid, port)
			return
		}
		// Re-run in the background, detached from this terminal, so that
		// `make ergo-up` returns.
		logPath := filepath.Join(os.TempDir(), fmt.Sprintf("chatterbox-ergo-dev-%d.log", os.Getuid()))
		log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			fail(err)
		}
		self, _ := os.Executable()
		proc, err := os.StartProcess(self, []string{self, "serve"}, &os.ProcAttr{
			Files: []*os.File{nil, log, log},
			Sys:   &syscall.SysProcAttr{Setsid: true},
		})
		if err != nil {
			fail(err)
		}
		proc.Release()
		for i := 0; i < 600; i++ { // the first run downloads Ergo
			if _, ok := running(); ok {
				addr := fmt.Sprintf("127.0.0.1:%d", port)
				fmt.Printf("Ergo %s is listening on %s (TLS, self-signed).\n", ergo.Version, addr)
				fmt.Printf("  capabilities:  printf 'CAP LS 302\\r\\nQUIT\\r\\n' | openssl s_client -quiet -connect %s\n", addr)
				fmt.Printf("  listener:      chatterbox listen --as agentA --channel '#test' --server 127.0.0.1 --port %d\n", port)
				fmt.Printf("  operator:      /oper %s %s\n", ergo.OperName, ergo.OperPass)
				fmt.Println("  stop with:     make ergo-down")
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		fail(fmt.Errorf("Ergo did not come up; see %s", logPath))
	case "serve":
		s, err := ergo.Start(ergo.Options{Port: port, Defaults: true})
		if err != nil {
			fail(err)
		}
		os.WriteFile(pidFile(), []byte(strconv.Itoa(os.Getpid())), 0o600)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		s.Stop()
		os.Remove(pidFile())
	case "down":
		pid, ok := running()
		if !ok {
			fmt.Println("no local Ergo is running")
			return
		}
		syscall.Kill(pid, syscall.SIGTERM)
		fmt.Println("stopped")
	}
}

// running reports the pid of the serving process, if it is alive.
func running() (int, bool) {
	data, err := os.ReadFile(pidFile())
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil || syscall.Kill(pid, 0) != nil {
		os.Remove(pidFile())
		return 0, false
	}
	return pid, true
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "ergo-dev:", err)
	os.Exit(1)
}
