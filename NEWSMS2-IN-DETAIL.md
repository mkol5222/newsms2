# newsms2 in detail

Exactly what `install`, `check` and `uninstall` do, how the boot hook works on Gaia, and how to verify each step. This describes the code in `admin.go` and `newsms2.init`. Short version: [NEWSMS2.md](NEWSMS2.md).

1. [install](#1-newsms2-install)
2. [The boot hook script](#2-the-boot-hook-etcinitdnewsms2)
3. [SysV / chkconfig on Gaia](#3-sysv-init-scripts-and-chkconfig-on-gaia)
4. [systemd compatibility](#4-systemd-compatibility-newer-gaia)
5. [Ordering vs Check Point startup](#5-ordering-vs-check-point-startup)
6. [What happens on reboot](#6-what-happens-on-reboot)
7. [check](#7-newsms2-check)
8. [uninstall](#8-newsms2-uninstall---purge)
9. [Verification checklist](#9-verification-checklist-expert-mode)
10. [Troubleshooting](#10-troubleshooting)

## 1. `./newsms2 install`

Run as root in expert mode, from any location (for example `/home/admin/newsms2` after `make upload`). Steps, in order:

1. **Preconditions.** The effective UID must be `0`. `cpwd_admin` must be found in `$PATH`, or else at `$CPDIR/bin/cpwd_admin`. If either check fails, install stops before changing anything.
2. **Config validation.** The config is loaded from `-conf` (default `/opt/newsms2/newsms2.conf`; a missing file means defaults), then any flags override it. The listen address must be loopback (`127.0.0.0/8`, `::1`, `localhost`) with port 1024–65535. `username`/`password` must be both set or both empty, and `handler_timeout` must be greater than 0. A config file readable by group or others (anything other than `chmod 600`) is rejected.
3. **Old registration removed (idempotency).** If `cpwd_admin list` already has a `NEWSMS2` row, install runs:
   ```sh
   /etc/init.d/newsms2 stop        # errors ignored
   cpwd_admin stop -name NEWSMS2
   cpwd_admin del  -name NEWSMS2
   sleep 2
   ```
   This is what makes it safe to run install again, for example to upgrade.
4. **Port conflict guard.** install briefly opens a listener on the configured address (default `127.0.0.1:60611`) and closes it again. If the port is taken (an old `newsms`, or an orphaned process), install **aborts** without writing anything. Two relays on one port cause the WatchDog restart loop (PID 0, STAT T, a rising #START count).
5. **Files.** Each file is written to `<path>.tmp`, then `chmod`, then `rename`, so it is replaced atomically and a running binary can be replaced safely:

   | Path | Mode | Behavior |
   |---|---|---|
   | `/opt/newsms2/` | 0755 | created if missing |
   | `/opt/newsms2/newsms2` | 0755 | copy of the running binary (skipped if install already runs from this path) |
   | `/opt/newsms2/sms-handler.sh` | 0755 | embedded mockup, written **only if missing**, so a customized handler is kept |
   | `/opt/newsms2/newsms2.conf` | 0600 | commented template, written **only if missing** |
   | `/etc/init.d/newsms2` | 0755 | embedded boot hook, **always overwritten** |

6. **Boot registration.** If `chkconfig` is in `$PATH`, install runs `chkconfig --add newsms2`, and a failure stops the install. If `chkconfig` is missing, this step is silently skipped (see [§3](#3-sysv-init-scripts-and-chkconfig-on-gaia)).
7. **Start.** install runs `/etc/init.d/newsms2 start` (see [§2](#2-the-boot-hook-etcinitdnewsms2)), which registers the process with cpwd. A non-zero exit stops the install.
8. **Report.** After 2 s, install prints the `NEWSMS2` row from `cpwd_admin list` and the DynamicID SMS provider string for the configured port:
   ```
   sms:http://127.0.0.1:60611/sendsms?username=$USERNAME&password=$PASSWORD&phone=$PHONE&smstext=$MESSAGE
   ```

> [!NOTE]
> The boot hook and cpwd always start `newsms2 serve` with **no flags**, so the relay reads only `/opt/newsms2/newsms2.conf`. A `-listen` or `-handler` flag given to `install` affects only the install-time checks and the printed string. To run on a non-default port or handler permanently, put it in `newsms2.conf`.

## 2. The boot hook: `/etc/init.d/newsms2`

A small bash script that registers one instance with cpwd. It loads `/etc/profile.d/CP.sh` (for `$CPDIR` and `cpwd_admin` in `PATH`), then:

**`start`**

1. Fails if `/opt/newsms2/newsms2` is not executable.
2. Waits for WatchDog: up to 60 × 5 s (5 min) for `cpwd_admin exist` to succeed, otherwise fails.
3. If a `NEWSMS2` row has PID ≠ 0 and STAT `E`, it is already running and nothing is done (exit 0).
4. If a stale row exists, runs `cpwd_admin stop -name NEWSMS2` and `cpwd_admin del -name NEWSMS2`.
5. Registers:
   ```sh
   cpwd_admin start -name NEWSMS2 -path /opt/newsms2/newsms2 \
     -command "newsms2 serve" -slp_timeout 5 -retry_limit u
   ```
   `-path` is the binary itself, not a shell wrapper; the old `sh -c` wrapper caused the 2025 restart storm. `-command` is the argv line (`argv[0]` = `newsms2`). `-slp_timeout 5` means wait 5 s between restarts. `-retry_limit u` means restart without limit.

| Action | What it does |
|---|---|
| `stop` | `cpwd_admin stop -name NEWSMS2`, then `cpwd_admin del -name NEWSMS2`. Always exits 0. |
| `restart` | `stop`, wait 2 s, `start` |
| `status` | prints the `NEWSMS2` row; exits 0 only if PID ≠ 0 and STAT `E` |

> [!WARNING]
> Unverified on R82.10: the exact `cpwd_admin` options (`del`, `-slp_timeout`, `-retry_limit u`) come from the earlier newsms runbook and scripts that ran on R82/R82.10. If `cpwd_admin start` rejects them, check `cpwd_admin -h` on the box.

## 3. SysV init scripts and chkconfig on Gaia

Gaia is Red Hat based and still supports classic SysV scripts in `/etc/rc.d/init.d/` (`/etc/init.d` is a symlink to it). `chkconfig` reads two headers in the script:

```sh
# chkconfig: 345 99 01
# description: newsms2 DynamicID SMS relay (registers with Check Point WatchDog)
```

- `345`: runlevels where the service is **on** (3, 4, 5).
- `99`: start priority, giving a `S99newsms2` link (runs late in the boot order).
- `01`: stop priority, giving a `K01newsms2` link (stops early at shutdown).

`chkconfig --add newsms2` creates these symlinks to `../init.d/newsms2`:

```
/etc/rc.d/rc3.d/S99newsms2   /etc/rc.d/rc4.d/S99newsms2   /etc/rc.d/rc5.d/S99newsms2
/etc/rc.d/rc0.d/K01newsms2   /etc/rc.d/rc1.d/K01newsms2   /etc/rc.d/rc2.d/K01newsms2   /etc/rc.d/rc6.d/K01newsms2
```

The script also has an LSB block (`### BEGIN INIT INFO`, with `Default-Start: 3 4 5` matching the chkconfig line). Newer chkconfig versions may compute priorities from the LSB dependencies instead of `99/01`, so the actual `S##` number can differ. Check with `ls /etc/rc.d/rc3.d | grep newsms2`.

Gaia boots to **runlevel 3** (multi-user, no GUI), so `S99newsms2` in `rc3.d` is the link that matters. Check with `runlevel` or `who -r`.

## 4. systemd compatibility (newer Gaia)

Recent Gaia (R81.20 and later; R82.10 runs a 5.14 kernel) boots with systemd. systemd does not run `rc3.d` directly. At boot and on every `systemctl daemon-reload`, `systemd-sysv-generator` turns each enabled `/etc/rc.d/init.d` script into a generated unit:

- `newsms2` becomes `newsms2.service`, generated under `/run/systemd/generator.late/` (not on disk under `/etc`).
- Enabled state comes from the `S##` links in `rcN.d` (so `chkconfig --add` is still what enables it), and ordering from the LSB header and the S/K numbers.
- The unit runs `/etc/init.d/newsms2 start` and treats the service as active once it exits successfully (`RemainAfterExit`). This fits the hook: it registers with cpwd and returns, and cpwd keeps the process alive.

```sh
systemctl status newsms2            # generated unit; "loaded (/etc/rc.d/init.d/newsms2; generated)"
systemctl cat newsms2               # shows the generated unit
chkconfig --list newsms2            # 3:on 4:on 5:on expected
journalctl -u newsms2 -b            # boot-time output of the hook
```

> [!WARNING]
> Unverified on R82.10: whether Gaia R82.10 really runs this through systemd's sysv-generator, and the generated unit's start timeout. Generated units commonly get a timeout of about 5 min, which is close to the hook's 5 min wait for cpwd. If `journalctl -u newsms2` shows a timeout at boot, shorten the wait loop or report it so it can be fixed.

## 5. Ordering vs Check Point startup

WatchDog (`cpwd`) is started by Check Point's own boot sequence (the Check Point init script / `cpboot`, which runs `cpstart`). It can come up several minutes after the OS reaches runlevel 3, especially on a management server. There is no declared dependency between `newsms2` and the Check Point scripts. `S99` only means "late", and under systemd, units can start in parallel. That's why `start` polls `cpwd_admin exist` before registering, instead of failing on the first try.

## 6. What happens on reboot

1. Shutdown: `K01newsms2 stop` deregisters `NEWSMS2`; cpwd and the Check Point processes then stop.
2. Boot: cpwd starts with no `NEWSMS2` entry, because `cpwd_admin start` registrations are not persistent.
3. `S99newsms2 start` (or `newsms2.service`) waits for cpwd, then registers one instance.
4. cpwd starts `/opt/newsms2/newsms2 serve`, which opens `127.0.0.1:60611` and writes a `listening on` line to `/var/log/newsms2.log`.

> [!NOTE]
> The hook runs only at boot. After a manual `cpstop; cpstart` (no reboot), cpwd may come back without the custom `NEWSMS2` entry (this hasn't been checked on R82.10). Run `/etc/init.d/newsms2 start` afterwards; `newsms2 check` will show if it's missing. Major upgrades and Jumbo Takes can remove custom files and rc links, so re-run `install` after them.

## 7. `./newsms2 check`

This is read-only except for the optional test SMS. Run it as root, so it can read the 0600 config and use its credentials. Each line prints `OK` or `FAIL`, and the exit code is 1 if anything failed.

| Line | Test |
|---|---|
| `cpwd` | `cpwd_admin` found; a `NEWSMS2` row exists with PID ≠ 0 and STAT `E` |
| `boot` | `/etc/init.d/newsms2` exists and is executable. It does **not** check the chkconfig links. |
| `handler` | the configured handler is a regular executable file |
| `listener` | `GET http://<listen>/health` returns 200 (`{"status":"ok","version":"…"}`) |
| `test sms` | `GET /sendsms` with `phone` (`-phone`, default `420000000000`), `smstext=newsms2 check <UTC time>`, and the config's username/password if set; expects 200 |

The HTTP timeout is the handler timeout + 5 s. With the mockup handler, the test SMS appears as a line in `/var/log/smsdelivered.log`. With a real provider handler it sends a **real SMS**, so use `-phone <your test number>` or `-no-send`.

## 8. `./newsms2 uninstall [--purge]`

Root and `cpwd_admin` are required. It undoes install in reverse order:

| Step | Command / effect | Undoes |
|---|---|---|
| 1 | `cpwd_admin stop -name NEWSMS2`; `cpwd_admin del -name NEWSMS2` (errors ignored). cpwd terminates the process; `serve` shuts down cleanly on SIGTERM. | install step 7 |
| 2 | `chkconfig --del newsms2` (if chkconfig exists), removing all `S##/K##newsms2` links | step 6 |
| 3 | `rm /etc/init.d/newsms2`. systemd drops the generated unit on its next `daemon-reload` or reboot. | step 5 (hook) |
| 4 | `rm -rf /opt/newsms2`: binary, handler **and config**, including any customized handler or credentials | step 5 (files) |
| 5 | default: logs kept. `--purge` also deletes `/var/log/newsms2.log*` and `/var/log/smsdelivered.log*`. | runtime logs |

Not touched: the DynamicID SMS provider setting in SmartConsole (change it and install policy), and the uploaded copy at `/home/admin/newsms2`.

## 9. Verification checklist (expert mode)

```sh
# install result
/opt/newsms2/newsms2 version
ls -l /opt/newsms2/ /etc/init.d/newsms2          # 0755 binary/handler, 0600 conf
cpwd_admin list | awk 'NR==1 || $1=="NEWSMS2"'    # PID != 0, STAT E, stable #START
netstat -lntp | grep ':60611 '                     # 127.0.0.1:60611 owned by newsms2

# boot persistence
chkconfig --list newsms2                           # 3:on 4:on 5:on
ls -l /etc/rc.d/rc3.d/ | grep newsms2              # S99newsms2 -> ../init.d/newsms2
systemctl status newsms2                           # if systemd: generated unit, active (exited)
/etc/init.d/newsms2 status; echo $?                # 0 = running

# function
/opt/newsms2/newsms2 check
curl_cli -s http://127.0.0.1:60611/health
tail -n 20 /var/log/newsms2.log                    # OK/FAIL lines, phone masked, no password
tail -n 5 /var/log/smsdelivered.log                # mockup deliveries

# reboot test (lab)
reboot   # then, after login:
cpwd_admin list | awk '$1=="NEWSMS2"'; journalctl -u newsms2 -b; /opt/newsms2/newsms2 check
```

## 10. Troubleshooting

| Symptom | Cause / action |
|---|---|
| install: `must run as root` | Enter `expert` mode first. |
| install: `cpwd_admin not found` | Not a Check Point box, or the CP environment isn't loaded: `. /etc/profile.d/CP.sh` |
| install: `... is already in use` | Something holds the port: `netstat -lntp \| grep :60611`. If it's the old `newsms`, stop it (`/etc/init.d/newsms stop`), confirm the PID is gone, then re-run install. |
| install: `permissions ... too open` | `chmod 600 /opt/newsms2/newsms2.conf` |
| cpwd row PID 0, STAT T, #START rising | Restart loop. Read `tail $CPDIR/log/cpwd.elg` and `/var/log/newsms2.log` (bad config or busy port). Fix, then `/etc/init.d/newsms2 restart`. |
| Not running after reboot | `chkconfig --list newsms2`; `journalctl -u newsms2 -b` (look for "WatchDog not running" or a timeout); then `/etc/init.d/newsms2 start`. |
| Not running after `cpstop; cpstart` | Expected (the hook runs at boot only): `/etc/init.d/newsms2 start` |
| check: `test sms` 401 | Credentials in `newsms2.conf` don't match. If you're not running check as root, the config can't be read and no credentials are sent. |
| check: `test sms` 502 | The handler failed or timed out; see the `FAIL` line in `/var/log/newsms2.log`. Run the handler by hand: `/opt/newsms2/sms-handler.sh 420000000000 test; echo $?` |
| DynamicID gets no SMS, but `check` passes | Check that the provider string in SmartConsole points to `127.0.0.1:60611/sendsms` and that policy was installed. A relay `OK` line means the handler succeeded; problems after that are on the provider side. |

*Custom field code, not a Check Point product. Items marked unverified should be confirmed on the target R82.10 build.*
