// newsms2 - DynamicID SMS relay for Check Point Gaia, supervised by cpwd (WatchDog).
//
// DynamicID calls http://127.0.0.1:<port>/sendsms?username=..&password=..&phone=..&smstext=..
// newsms2 validates the request and runs the handler script: <handler> <phone> <message>.
package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

const usage = `newsms2 %s - DynamicID SMS relay (cpwd supervised, loopback only)

usage:
  newsms2 serve     [flags]   run the relay (this is what cpwd starts)
  newsms2 install   [flags]   install to /opt/newsms2, register with cpwd, add boot hook
  newsms2 uninstall [--purge] stop, deregister, remove files (--purge also logs)
  newsms2 check     [flags]   verify cpwd, listener, handler; send a test SMS
  newsms2 version

flags (serve/install/check):
  -conf     config file              (default /opt/newsms2/newsms2.conf)
  -listen   loopback address:port    (default 127.0.0.1:60611)
  -handler  handler executable       (default /opt/newsms2/sms-handler.sh)
  -log      log file, "-" = stderr   (default /var/log/newsms2.log)
check only:
  -phone    test destination         (default 420000000000)
  -no-send  skip the test SMS
`

func main() {
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, usage, version) }
	conf := fs.String("conf", defaultConf, "")
	listen := fs.String("listen", "", "")
	handler := fs.String("handler", "", "")
	logPath := fs.String("log", "", "")
	timeout := fs.Duration("timeout", 0, "")
	phone := fs.String("phone", "420000000000", "")
	noSend := fs.Bool("no-send", false, "")
	purge := fs.Bool("purge", false, "")

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fs.Usage()
		return
	}
	if cmd == "version" {
		fmt.Println("newsms2", version)
		return
	}
	fs.Parse(args)

	load := func() Config {
		c, err := loadConfig(*conf)
		if err != nil {
			fail(err)
		}
		if *listen != "" {
			c.Listen = *listen
		}
		if *handler != "" {
			c.Handler = *handler
		}
		if *logPath != "" {
			c.Log = *logPath
		}
		if *timeout > 0 {
			c.HandlerTimeout = *timeout
		}
		return c
	}

	var err error
	switch cmd {
	case "serve":
		err = serve(load())
	case "install":
		err = install(load())
	case "uninstall":
		err = uninstall(*purge)
	case "check":
		err = check(load(), *phone, !*noSend)
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "newsms2: %v\n", err)
	os.Exit(1)
}
