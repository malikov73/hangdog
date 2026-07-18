//go:build unix

package hangdog_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEndToEnd builds hangdog and runs it against the hanging fixture, asserting
// that hangdog fires on the idle trigger and names the hung test — well before
// any go test timeout (the run uses -timeout 0).
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e builds and runs go test; skipped in -short")
	}

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "hangdog")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/hangdog").CombinedOutput(); err != nil {
		t.Fatalf("build hangdog: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, bin, "--idle", "2s", "--", "go", "test", "./...")
	cmd.Dir = filepath.Join(root, "testdata", "hang")
	out, _ := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("hangdog did not stop the hung run within 30s\n%s", out)
	}
	got := string(out)
	if !strings.Contains(got, "HUNG:") || !strings.Contains(got, "TestHang") {
		t.Fatalf("expected a HUNG report naming TestHang, got:\n%s", got)
	}
	if elapsed > 20*time.Second {
		t.Errorf("hangdog took %s; idle trigger (2s) should fire much sooner", elapsed)
	}
	t.Logf("hangdog reported the hang in %s", elapsed.Round(time.Millisecond))
}
