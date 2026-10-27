// Command lisimflix is the M1 executable: init provisions the config, serve
// runs the app, healthcheck probes readiness, backup-now takes a snapshot.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: lisimflix init|serve|healthcheck|backup-now [flags]")
		return 2
	}
	var err error
	switch args[0] {
	case "init":
		err = cmdInit(args[1:], stdin, stdout)
	case "serve":
		err = cmdServe(args[1:])
	case "healthcheck":
		err = cmdHealthcheck(args[1:])
	case "backup-now":
		err = cmdBackupNow(args[1:])
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, "lisimflix:", err)
		return 1
	}
	return 0
}

// storagePaths fills unset database and backup paths from the config's
// folder, so tests and other layouts need no extra flags.
func storagePaths(cfgPath, db, backups string) (string, string) {
	dir := filepath.Dir(cfgPath)
	if db == "" {
		db = filepath.Join(dir, "lisimflix.db")
	}
	if backups == "" {
		backups = filepath.Join(dir, "backups")
	}
	return db, backups
}

func cmdHealthcheck(args []string) error {
	fl := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	addr := fl.String("addr", "127.0.0.1:8420", "loopback address of the server")
	if err := fl.Parse(args); err != nil {
		return err
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + *addr + "/health/ready")
	if err != nil {
		return errors.New("not reachable")
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("not ready (%d)", resp.StatusCode)
	}
	return nil
}
