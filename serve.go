package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxMessage = 1024

var phoneRE = regexp.MustCompile(`^\+?[0-9]{6,20}$`)

// validateSMS checks the DynamicID request values before anything is executed.
func validateSMS(phone, text string) error {
	if !phoneRE.MatchString(phone) {
		return errors.New("invalid phone (digits only, 6-20, optional leading +)")
	}
	if text == "" || len(text) > maxMessage || !utf8.ValidString(text) {
		return fmt.Errorf("invalid message (1-%d bytes UTF-8)", maxMessage)
	}
	if strings.ContainsRune(text, 0) {
		return errors.New("invalid message (NUL byte)")
	}
	return nil
}

// maskPhone keeps the last 3 digits: 420000000123 -> *********123
func maskPhone(p string) string {
	if len(p) <= 3 {
		return "***"
	}
	return strings.Repeat("*", len(p)-3) + p[len(p)-3:]
}

// maskUser keeps the first character: smsuser -> s******
func maskUser(u string) string {
	if u == "" {
		return "-"
	}
	r, size := utf8.DecodeRuneInString(u)
	return string(r) + strings.Repeat("*", utf8.RuneCountInString(u[size:]))
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

type relay struct {
	cfg Config
	log *log.Logger
	sem chan struct{} // limits concurrent handler runs
}

func (s *relay) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/sendsms", s.sendSMS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok","version":"`+version+`"}`+"\n")
	})
	return mux
}

func (s *relay) sendSMS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "GET or POST required", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	user := r.Form.Get("username")
	pass := r.Form.Get("password") // never logged
	phone := r.Form.Get("phone")
	text := r.Form.Get("smstext")

	if s.cfg.Username != "" && !(eq(user, s.cfg.Username) && eq(pass, s.cfg.Password)) {
		s.log.Printf("reject user=%s phone=%s: bad credentials", maskUser(user), maskPhone(phone))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := validateSMS(phone, text); err != nil {
		s.log.Printf("reject user=%s phone=%s: %v", maskUser(user), maskPhone(phone), err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-r.Context().Done():
		return
	}

	start := time.Now()
	out, err := s.runHandler(r.Context(), phone, text)
	took := time.Since(start).Round(time.Millisecond)
	if err != nil {
		s.log.Printf("FAIL user=%s phone=%s msg_bytes=%d took=%s: %v output=%q",
			maskUser(user), maskPhone(phone), len(text), took, err, trim(out, 200))
		http.Error(w, "delivery failed", http.StatusBadGateway)
		return
	}
	s.log.Printf("OK user=%s phone=%s msg_bytes=%d took=%s", maskUser(user), maskPhone(phone), len(text), took)
	io.WriteString(w, "OK\n")
}

// runHandler executes the handler directly (no shell): handler <phone> <message>.
func (s *relay) runHandler(ctx context.Context, phone, text string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.HandlerTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.cfg.Handler, phone, text)
	cmd.Dir = filepath.Dir(s.cfg.Handler)
	cmd.Env = append(os.Environ(), "NEWSMS_PHONE="+phone, "NEWSMS_SMSTEXT="+text)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("handler timeout after %s", s.cfg.HandlerTimeout)
	}
	return string(out), err
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func newLogger(c Config) (*log.Logger, io.Closer, error) {
	if c.Log == "-" {
		return log.New(os.Stderr, "", log.LstdFlags|log.LUTC), io.NopCloser(nil), nil
	}
	rf, err := openRotating(c.Log, c.LogMaxBytes, c.LogKeep)
	if err != nil {
		return nil, nil, err
	}
	return log.New(rf, "", log.LstdFlags|log.LUTC), rf, nil
}

func serve(c Config) error {
	if err := c.validate(); err != nil {
		return err
	}
	lg, closer, err := newLogger(c)
	if err != nil {
		return fmt.Errorf("log %s: %v", c.Log, err)
	}
	defer closer.Close()

	s := &relay{cfg: c, log: lg, sem: make(chan struct{}, 4)}
	srv := &http.Server{
		Addr:              c.Listen,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      c.HandlerTimeout + 10*time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          lg,
	}

	auth := "none (any username/password accepted)"
	if c.Username != "" {
		auth = "provider account " + maskUser(c.Username)
	}
	lg.Printf("newsms2 %s listening on http://%s handler=%s auth=%s", version, c.Listen, c.Handler, auth)

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	select {
	case err := <-errc:
		lg.Printf("listener stopped: %v", err)
		return err
	case got := <-sig:
		lg.Printf("signal %s, shutting down", got)
		ctx, cancel := context.WithTimeout(context.Background(), c.HandlerTimeout)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}
