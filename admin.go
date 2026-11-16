package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed sms-handler.sh
var handlerScript []byte

//go:embed newsms2.init
var initScript []byte

const (
	installDir = "/opt/newsms2"
	installBin = installDir + "/newsms2"
	initPath   = "/etc/init.d/newsms2"
	cpwdName   = "NEWSMS2"
)

const confTemplate = `# newsms2 configuration (key=value). Must stay chmod 600.
# listen          = 127.0.0.1:60611
# handler         = /opt/newsms2/sms-handler.sh
# handler_timeout = 30s
# log             = /var/log/newsms2.log
# log_max_mb      = 5
# log_keep        = 5
#
# Optional: require the DynamicID SMS provider account ($USERNAME / $PASSWORD in the
# SMS URL). Leave both unset to accept any values.
# username = <provider-account>
# password = <provider-password>
`

func say(format string, a ...any) { fmt.Printf("  "+format+"\n", a...) }

func cpwdAdmin() (string, error) {
	if p, err := exec.LookPath("cpwd_admin"); err == nil {
		return p, nil
	}
	if d := os.Getenv("CPDIR"); d != "" {
		if p := filepath.Join(d, "bin", "cpwd_admin"); isExec(p) {
			return p, nil
		}
	}
	return "", errors.New("cpwd_admin not found - run in expert mode on a Check Point management/gateway")
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0
}

func requireGaia() (string, error) {
	if os.Geteuid() != 0 {
		return "", errors.New("must run as root (expert mode)")
	}
	return cpwdAdmin()
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// cpwdRow returns the fields of the NEWSMS2 line in `cpwd_admin list` (APP PID STAT #START ...).
func cpwdRow(cpwd string) []string {
	out, _ := run(cpwd, "list")
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) >= 3 && f[0] == cpwdName {
			return f
		}
	}
	return nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func install(c Config) error {
	cpwd, err := requireGaia()
	if err != nil {
		return err
	}
	if err := c.validate(); err != nil {
		return err
	}
	fmt.Println("newsms2 install")

	// stop a previous registration first: two instances on one port = restart storm
	if cpwdRow(cpwd) != nil {
		run(initPath, "stop")
		run(cpwd, "stop", "-name", cpwdName)
		run(cpwd, "del", "-name", cpwdName)
		say("stopped previous %s registration", cpwdName)
		time.Sleep(2 * time.Second)
	}
	if l, err := net.Listen("tcp", c.Listen); err != nil {
		return fmt.Errorf("%s is already in use by another process (old newsms?) - stop it first: %v", c.Listen, err)
	} else {
		l.Close()
	}

	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, _ = filepath.EvalSymlinks(self); self != installBin {
		data, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(installBin, data, 0o755); err != nil {
			return err
		}
	}
	say("binary   %s", installBin)

	if _, err := os.Stat(defaultHandler); os.IsNotExist(err) {
		if err := writeFileAtomic(defaultHandler, handlerScript, 0o755); err != nil {
			return err
		}
		say("handler  %s (mockup -> /var/log/smsdelivered.log)", defaultHandler)
	} else {
		say("handler  %s (kept existing)", defaultHandler)
	}
	if _, err := os.Stat(defaultConf); os.IsNotExist(err) {
		if err := writeFileAtomic(defaultConf, []byte(confTemplate), 0o600); err != nil {
			return err
		}
		say("config   %s (defaults, no credential check)", defaultConf)
	} else {
		say("config   %s (kept existing)", defaultConf)
	}

	if err := writeFileAtomic(initPath, initScript, 0o755); err != nil {
		return err
	}
	if _, err := exec.LookPath("chkconfig"); err == nil {
		if out, err := run("chkconfig", "--add", "newsms2"); err != nil {
			return fmt.Errorf("chkconfig --add newsms2: %v %s", err, out)
		}
	}
	say("boot     %s (chkconfig, re-registers with cpwd after reboot)", initPath)

	if out, err := run(initPath, "start"); err != nil {
		return fmt.Errorf("start: %v %s", err, out)
	}
	time.Sleep(2 * time.Second)
	if f := cpwdRow(cpwd); f != nil {
		say("cpwd     %s", strings.Join(f, " "))
	}

	_, port, _ := net.SplitHostPort(c.Listen)
	fmt.Printf(`
DynamicID - SmartConsole > gateway object > VPN Clients > Authentication > DynamicID Settings
  (or Global Properties > Remote Access > SecurID/DynamicID) - SMS Provider:

  sms:http://127.0.0.1:%s/sendsms?username=$USERNAME&password=$PASSWORD&phone=$PHONE&smstext=$MESSAGE

Then install policy and run: newsms2 check
`, port)
	return nil
}

func uninstall(purge bool) error {
	cpwd, err := requireGaia()
	if err != nil {
		return err
	}
	fmt.Println("newsms2 uninstall")
	run(cpwd, "stop", "-name", cpwdName)
	run(cpwd, "del", "-name", cpwdName)
	say("cpwd     %s stopped and removed", cpwdName)
	if _, err := exec.LookPath("chkconfig"); err == nil {
		run("chkconfig", "--del", "newsms2")
	}
	os.Remove(initPath)
	say("boot     %s removed", initPath)
	if err := os.RemoveAll(installDir); err != nil {
		return err
	}
	say("files    %s removed", installDir)
	if purge {
		for _, pat := range []string{defaultLog + "*", "/var/log/smsdelivered.log*"} {
			m, _ := filepath.Glob(pat)
			for _, f := range m {
				os.Remove(f)
			}
		}
		say("logs     removed")
	} else {
		say("logs     kept: %s*, /var/log/smsdelivered.log* (use --purge)", defaultLog)
	}
	return nil
}

func check(c Config, phone string, send bool) error {
	if err := c.validate(); err != nil {
		return err
	}
	fmt.Println("newsms2 check")
	failed := false
	mark := func(ok bool, what, detail string) {
		st := "OK  "
		if !ok {
			st, failed = "FAIL", true
		}
		fmt.Printf("  %s %-10s %s\n", st, what, detail)
	}

	if cpwd, err := cpwdAdmin(); err != nil {
		mark(false, "cpwd", err.Error())
	} else if f := cpwdRow(cpwd); f == nil {
		mark(false, "cpwd", cpwdName+" not registered (run: newsms2 install)")
	} else {
		mark(f[1] != "0" && f[2] == "E", "cpwd", strings.Join(f, " "))
	}
	if st, err := os.Stat(initPath); err != nil || st.Mode()&0o111 == 0 {
		mark(false, "boot", initPath+" missing")
	} else {
		mark(true, "boot", initPath)
	}
	mark(isExec(c.Handler), "handler", c.Handler)

	cl := &http.Client{Timeout: c.HandlerTimeout + 5*time.Second}
	// plain HTTP is loopback-only: c.Listen is enforced to 127.0.0.0/8 / ::1 by validate()
	base := "http://" + c.Listen
	if resp, err := cl.Get(base + "/health"); err != nil {
		mark(false, "listener", err.Error())
	} else {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		mark(resp.StatusCode == 200, "listener", base+" "+strings.TrimSpace(string(body)))
	}

	if send {
		q := url.Values{"phone": {phone}, "smstext": {"newsms2 check " + time.Now().UTC().Format(time.RFC3339)}}
		if c.Username != "" {
			q.Set("username", c.Username)
			q.Set("password", c.Password)
		}
		if resp, err := cl.Get(base + "/sendsms?" + q.Encode()); err != nil {
			mark(false, "test sms", err.Error())
		} else {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			mark(resp.StatusCode == 200, "test sms", fmt.Sprintf("to %s -> %d %s", maskPhone(phone), resp.StatusCode, strings.TrimSpace(string(body))))
		}
	}
	if failed {
		return errors.New("check failed")
	}
	return nil
}
