package tlspin

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// EntryCA marks a server whose certificate verified against the roots.
const EntryCA = "ca"

// Store is the trust file: one line per host:port, holding either "ca" or a
// fingerprint.
type Store struct{ Path string }

func (s Store) read() (map[string]string, []string, error) {
	entries := map[string]string{}
	var order []string
	f, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return entries, order, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, nil, fmt.Errorf("%s:%d: expected \"host:port ca|fingerprint\"", s.Path, n)
		}
		val := fields[1]
		if val != EntryCA {
			fp, err := Normalize(val)
			if err != nil {
				return nil, nil, fmt.Errorf("%s:%d: %v", s.Path, n, err)
			}
			val = fp
		}
		if _, dup := entries[fields[0]]; !dup {
			order = append(order, fields[0])
		}
		entries[fields[0]] = val
	}
	return entries, order, sc.Err()
}

// Lookup returns the entry for hostport.
func (s Store) Lookup(hostport string) (entry string, found bool, err error) {
	entries, _, err := s.read()
	if err != nil {
		return "", false, err
	}
	entry, found = entries[hostport]
	return entry, found, nil
}

// Record adds an entry for hostport unless one exists already; an existing
// entry is never replaced. The file is rewritten atomically under a lock.
func (s Store) Record(hostport, entry string) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	entries, order, err := s.read()
	if err != nil {
		return err
	}
	if _, exists := entries[hostport]; exists {
		return nil
	}
	entries[hostport] = entry
	order = append(order, hostport)

	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".known_servers-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	w := bufio.NewWriter(tmp)
	for _, hp := range order {
		v := entries[hp]
		if v != EntryCA {
			v = "SHA256:" + v
		}
		fmt.Fprintf(w, "%s %s\n", hp, v)
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}
