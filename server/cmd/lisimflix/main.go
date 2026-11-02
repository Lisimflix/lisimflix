// Command lisimflix is the M1 executable: init provisions the config, serve
// runs the app, healthcheck probes readiness, backup-now takes a snapshot,
// library adds and lists libraries.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Lisimflix/lisimflix/server/internal/m1config"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: lisimflix init|serve|healthcheck|backup-now|library [flags]")
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
	case "library":
		err = cmdLibrary(args[1:], stdout)
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

// cmdHealthcheck asks the local server for readiness, at the listen address
// in the config (an empty or unspecified host means loopback), or at --addr.
func cmdHealthcheck(args []string) error {
	fl := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	cfgPath := fl.String("config", "/config/m1.json", "config file, for the listen address")
	addr := fl.String("addr", "", "address to check instead of the config's listen address")
	if err := fl.Parse(args); err != nil {
		return err
	}
	target := *addr
	if target == "" {
		cfg, err := m1config.Load(*cfgPath)
		if err != nil {
			return err
		}
		host, port, err := net.SplitHostPort(cfg.ListenAddr)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
			host = "127.0.0.1"
		}
		target = net.JoinHostPort(host, port)
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + target + "/health/ready")
	if err != nil {
		return errors.New("not reachable")
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("not ready (%d)", resp.StatusCode)
	}
	return nil
}
