//go:build unix

package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// TestBinaries returns every descendant of parent whose command name ends in
// ".test", annotated with its working directory. The cwd is the package's
// source directory, which is how a hung package (known from its test2json
// import path) is matched to the process that must receive SIGQUIT.
func TestBinaries(parent int) ([]TestBinary, error) {
	tree, err := processTree()
	if err != nil {
		return nil, err
	}

	var out []TestBinary
	queue := []int{parent}
	seen := map[int]bool{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range tree[cur] {
			if seen[c.pid] {
				continue
			}
			seen[c.pid] = true
			if strings.HasSuffix(c.comm, ".test") {
				out = append(out, TestBinary{
					PID:  c.pid,
					Name: filepath.Base(c.comm),
					Cwd:  cwd(c.pid),
				})
			}
			queue = append(queue, c.pid)
		}
	}
	return out, nil
}

type procRow struct {
	pid, ppid int
	comm      string
}

// processTree maps a parent PID to its direct children via `ps`.
func processTree() (map[int][]procRow, error) {
	out, err := exec.Command("ps", "-eo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	tree := map[int][]procRow{}
	for _, ln := range strings.Split(string(out), "\n") {
		f := strings.Fields(ln)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		comm := strings.Join(f[2:], " ")
		tree[ppid] = append(tree[ppid], procRow{pid: pid, ppid: ppid, comm: comm})
	}
	return tree, nil
}

// cwd returns the working directory of pid: /proc on Linux, lsof on macOS/BSD.
func cwd(pid int) string {
	if runtime.GOOS == "linux" {
		if dir, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd"); err == nil {
			return dir
		}
		return ""
	}
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(ln, "n") {
			return ln[1:]
		}
	}
	return ""
}
