package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for ffprobe: Runner.Path points at
// the binary and FAKE_FFPROBE picks what the fake does.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_FFPROBE"); mode != "" {
		os.Exit(fakeFFprobe(mode))
	}
	os.Exit(m.Run())
}

func fakeFFprobe(mode string) int {
	switch mode {
	case "echo": // proves ffprobe gets the opened file as /dev/fd/3, not a path
		if os.Args[len(os.Args)-1] != "/dev/fd/3" {
			fmt.Fprintln(os.Stderr, "input is not /dev/fd/3:", os.Args[1:])
			return 2
		}
		b, err := io.ReadAll(os.NewFile(3, "input"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Printf(`{"format":{"format_name":%q,"duration":"1.5"}}`, string(b))
	case "slow":
		time.Sleep(time.Minute)
	case "flood":
		os.Stdout.Write(make([]byte, 2<<10))
		time.Sleep(time.Minute)
	case "noisy": // damaged-but-playable files can print lots of -v error lines
		os.Stderr.Write(bytes.Repeat([]byte("x"), 5000))
		fmt.Print(`{"format":{"format_name":"matroska"}}`)
	case "fail":
		fmt.Fprintln(os.Stderr, "/dev/fd/3: Invalid data found when processing input")
		return 1
	}
	return 0
}

func fakeRunner(t *testing.T, mode string) (Runner, *os.Root) {
	t.Helper()
	t.Setenv("FAKE_FFPROBE", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), []byte("matroska"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return Runner{Path: exe, Timeout: 5 * time.Second}, root
}

func TestRunnerReadsTheOpenedFile(t *testing.T) {
	r, root := fakeRunner(t, "echo")
	res, err := r.Probe(context.Background(), root, "movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if res.Container != "matroska" || res.DurationMs != 1500 {
		t.Fatalf("got %+v", res)
	}
}

func TestRunnerRefusesPathsOutsideTheRoot(t *testing.T) {
	r, root := fakeRunner(t, "echo")
	outside := filepath.Join(t.TempDir(), "secret.mkv")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root.Name(), "link.mkv")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../secret.mkv", "link.mkv", outside} {
		if _, err := r.Probe(context.Background(), root, rel); err == nil {
			t.Errorf("%s: want an error", rel)
		}
	}
}

func TestRunnerTimeout(t *testing.T) {
	r, root := fakeRunner(t, "slow")
	r.Timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := r.Probe(context.Background(), root, "movie.mkv")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("ffprobe was not stopped promptly: %v", time.Since(start))
	}
}

func TestRunnerOutputLimit(t *testing.T) {
	r, root := fakeRunner(t, "flood")
	r.MaxOutput = 1 << 10
	start := time.Now()
	_, err := r.Probe(context.Background(), root, "movie.mkv")
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("want ErrOutputTooLarge, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("ffprobe was not stopped promptly: %v", time.Since(start))
	}
}

func TestRunnerIgnoresNoisyStderr(t *testing.T) {
	r, root := fakeRunner(t, "noisy")
	res, err := r.Probe(context.Background(), root, "movie.mkv")
	if err != nil || res.Container != "matroska" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestRunnerCancelStopsFFprobe(t *testing.T) {
	r, root := fakeRunner(t, "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := r.Probe(ctx, root, "movie.mkv")
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeout) {
		t.Fatalf("want the caller's context error, got %v", err)
	}
}

func TestRunnerReportsFFprobeFailure(t *testing.T) {
	r, root := fakeRunner(t, "fail")
	_, err := r.Probe(context.Background(), root, "movie.mkv")
	if err == nil || !strings.Contains(err.Error(), "Invalid data") {
		t.Fatalf("want ffprobe's message, got %v", err)
	}
}

func TestRunnerReportsMissingBinary(t *testing.T) {
	r, root := fakeRunner(t, "echo")
	r.Path = filepath.Join(t.TempDir(), "no-ffprobe")
	if _, err := r.Probe(context.Background(), root, "movie.mkv"); err == nil {
		t.Fatal("want an error")
	}
}
