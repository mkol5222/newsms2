package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultConf    = "/opt/newsms2/newsms2.conf"
	defaultListen  = "127.0.0.1:60611"
	defaultHandler = "/opt/newsms2/sms-handler.sh"
	defaultLog     = "/var/log/newsms2.log"
)

// Config is read from a simple key=value file (# comments). Absent file = defaults.
type Config struct {
	Listen         string        // must be a loopback address
	Handler        string        // executable called as: handler <phone> <message>
	Log            string        // relay log, "-" = stderr
	HandlerTimeout time.Duration // per SMS
	LogMaxBytes    int64
	LogKeep        int
	// Optional DynamicID provider account ($USERNAME/$PASSWORD in the SMS URL).
	// Both empty = requests are accepted without credential check.
	Username string
	Password string
}

func defaults() Config {
	return Config{
		Listen:         defaultListen,
		Handler:        defaultHandler,
		Log:            defaultLog,
		HandlerTimeout: 30 * time.Second,
		LogMaxBytes:    5 << 20,
		LogKeep:        5,
	}
}

// loadConfig merges the file at path over defaults. A missing file is not an error.
// The file may hold the provider password, so it must not be group/other accessible.
func loadConfig(path string) (Config, error) {
	c := defaults()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return c, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return c, fmt.Errorf("%s: permissions %04o too open, run: chmod 600 %s", path, st.Mode().Perm(), path)
	}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("%s:%d: expected key=value", path, n)
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "listen":
			c.Listen = v
		case "handler":
			c.Handler = v
		case "log":
			c.Log = v
		case "handler_timeout":
			if c.HandlerTimeout, err = time.ParseDuration(v); err != nil {
				return c, fmt.Errorf("%s:%d: %v", path, n, err)
			}
		case "log_max_mb":
			mb, err := strconv.Atoi(v)
			if err != nil || mb < 1 {
				return c, fmt.Errorf("%s:%d: log_max_mb must be a positive integer", path, n)
			}
			c.LogMaxBytes = int64(mb) << 20
		case "log_keep":
			if c.LogKeep, err = strconv.Atoi(v); err != nil || c.LogKeep < 1 {
				return c, fmt.Errorf("%s:%d: log_keep must be a positive integer", path, n)
			}
		case "username":
			c.Username = v
		case "password":
			c.Password = v
		default:
			return c, fmt.Errorf("%s:%d: unknown key %q", path, n, k)
		}
	}
	return c, sc.Err()
}

// validate enforces the loopback-only listener and sane values.
func (c Config) validate() error {
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen %q: %v", c.Listen, err)
	}
	ip := net.ParseIP(host)
	if host == "localhost" {
		ip = net.IPv4(127, 0, 0, 1)
	}
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen %q: only loopback addresses are allowed", c.Listen)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1024 || p > 65535 {
		return fmt.Errorf("listen %q: port must be 1024-65535", c.Listen)
	}
	if (c.Username == "") != (c.Password == "") {
		return fmt.Errorf("set both username and password, or neither")
	}
	if c.HandlerTimeout <= 0 {
		return fmt.Errorf("handler_timeout must be > 0")
	}
	return nil
}
