#!/bin/bash
# newsms2 mockup SMS handler.
#
# Contract: newsms2 runs   sms-handler.sh <phone> <message>
#   (the same values are also in env NEWSMS_PHONE / NEWSMS_SMSTEXT)
#   exit 0  = delivered  -> newsms2 answers DynamicID with 200
#   exit !0 = failed     -> newsms2 answers 502
#
# This mockup only appends one line to $SMSDELIVERED_LOG (default /var/log/smsdelivered.log)
# and rotates it by size (.1 .. .3). Replace the "deliver" part with a real provider call.
# NOTE: the log contains the OTP message text - lab/mockup use only.
set -u

PHONE="${1:-${NEWSMS_PHONE:-}}"
MSG="${2:-${NEWSMS_SMSTEXT:-}}"
LOG="${SMSDELIVERED_LOG:-/var/log/smsdelivered.log}"
MAX_BYTES="${SMSDELIVERED_MAX_BYTES:-1048576}"
KEEP=3

if [ -z "$PHONE" ] || [ -z "$MSG" ]; then
  echo "usage: $0 <phone> <message>" >&2
  exit 2
fi

umask 077

# serialize concurrent deliveries (flock exists on Gaia; skipped where missing)
exec 9>>"$LOG.lock"
command -v flock >/dev/null 2>&1 && flock -w 5 9

size=$(stat -c %s "$LOG" 2>/dev/null || stat -f %z "$LOG" 2>/dev/null || echo 0)
if [ "$size" -ge "$MAX_BYTES" ]; then
  i=$((KEEP - 1))
  while [ "$i" -ge 1 ]; do
    [ -f "$LOG.$i" ] && mv -f "$LOG.$i" "$LOG.$((i + 1))"
    i=$((i - 1))
  done
  mv -f "$LOG" "$LOG.1"
fi

# keep it one line per SMS
MSG_ONE=$(printf '%s' "$MSG" | tr '\r\n' '  ')
printf '%s phone=%s msg=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$PHONE" "$MSG_ONE" >>"$LOG" || exit 1

echo "delivered (mockup)"
exit 0
