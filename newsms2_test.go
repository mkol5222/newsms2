package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateSMS(t *testing.T) {
	ok := [][2]string{{"420000000123", "otp 123456"}, {"+420000000123", "x"}}
	bad := [][2]string{{"", "x"}, {"12345", "x"}, {"42000000012a", "x"}, {"420000000123", ""},
		{"420000000123", strings.Repeat("a", maxMessage+1)}, {"420000000123;id", "x"}, {"420000000123", "a\x00b"}}
	for _, c := range ok {
		if err := validateSMS(c[0], c[1]); err != nil {
			t.Errorf("want ok %q: %v", c, err)
		}
	}
	for _, c := range bad {
		if validateSMS(c[0], c[1]) == nil {
			t.Errorf("want error %q", c)
		}
	}
}

func TestMask(t *testing.T) {
	if got := maskPhone("420000000123"); got != "*********123" {
		t.Errorf("maskPhone = %q", got)
	}
	if got := maskUser("smsuser"); got != "s******" {
		t.Errorf("maskUser = %q", got)
	}
}

func TestValidateListen(t *testing.T) {
	for _, l := range []string{"127.0.0.1:60611", "localhost:60611", "[::1]:60611"} {
		c := defaults()
		c.Listen = l
		if err := c.validate(); err != nil {
			t.Errorf("%s: %v", l, err)
		}
	}
	for _, l := range []string{"0.0.0.0:60611", ":60611", "10.0.0.1:60611", "127.0.0.1:80"} {
		c := defaults()
		c.Listen = l
		if c.validate() == nil {
			t.Errorf("%s: want error", l)
		}
	}
}

func TestConfigPerms(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.conf")
	os.WriteFile(p, []byte("username=u\npassword=p\n"), 0o644)
	if _, err := loadConfig(p); err == nil {
		t.Fatal("want permission error for 0644")
	}
	os.Chmod(p, 0o600)
	c, err := loadConfig(p)
	if err != nil || c.Username != "u" || c.Password != "p" {
		t.Fatalf("load: %+v %v", c, err)
	}
}

func TestRotation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.log")
	r, err := openRotating(p, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n" // 40 bytes
	for i := 0; i < 10; i++ {
		r.Write([]byte(line))
	}
	r.Close()
	for _, f := range []string{p, p + ".1", p + ".2"} {
		st, err := os.Stat(f)
		if err != nil || st.Size() > 100 {
			t.Errorf("%s: %v size=%v", f, err, st)
		}
	}
	if _, err := os.Stat(p + ".3"); err == nil {
		t.Error("keep=2 exceeded")
	}
}

func TestSendSMSHandler(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	h := filepath.Join(dir, "h.sh")
	os.WriteFile(h, []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%s|%%s' \"$1\" \"$2\" > %s\n", out)), 0o755)
	c := defaults()
	c.Handler, c.HandlerTimeout, c.Username, c.Password = h, 5*time.Second, "u", "p"
	s := &relay{cfg: c, log: log.New(io.Discard, "", 0), sem: make(chan struct{}, 1)}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	get := func(q string) int {
		resp, err := http.Get(srv.URL + "/sendsms?" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := get("username=u&password=bad&phone=420000000123&smstext=x"); code != 401 {
		t.Errorf("bad creds: %d", code)
	}
	if code := get("username=u&password=p&phone=abc&smstext=x"); code != 400 {
		t.Errorf("bad phone: %d", code)
	}
	if code := get("username=u&password=p&phone=420000000123&smstext=Your+code+%3B+%24%28id%29"); code != 200 {
		t.Fatalf("ok: %d", code)
	}
	b, _ := os.ReadFile(out)
	if string(b) != "420000000123|Your code ; $(id)" { // args passed verbatim, no shell
		t.Errorf("handler args = %q", b)
	}
}
