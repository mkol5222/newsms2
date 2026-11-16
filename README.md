# newsms2

DynamicID SMS relay for Check Point Gaia: one static Go binary (linux/amd64, no dependencies), supervised by WatchDog (cpwd), listening on `127.0.0.1:60611` only.

> Custom field code (demo/concept), not a Check Point product. Validate in a lab first.

## Quick start

Download `newsms2` from [Releases](https://github.com/mkol5222/newsms2/releases), copy it to the gateway, then in expert mode as root:

```sh
chmod +x ./newsms2
./newsms2 install
./newsms2 check
```

Set the DynamicID SMS provider to:

```
sms:http://127.0.0.1:60611/sendsms?username=$USERNAME&password=$PASSWORD&phone=$PHONE&smstext=$MESSAGE
```

## Build from source

```sh
make test
make build    # dist/newsms2 (CGO_ENABLED=0, static linux/amd64)
```

## Docs

- [NEWSMS2.md](NEWSMS2.md): overview, install, config, handler contract, troubleshooting
- [NEWSMS2-IN-DETAIL.md](NEWSMS2-IN-DETAIL.md): what `install` / `check` / `uninstall` do step by step, boot hook, verification checklist
