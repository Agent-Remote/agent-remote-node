#!/bin/sh
# Install only inside the disposable diagnostic image. Never print commands, paths or tool output.
set -eu
observation=/var/tmp/agent-remote-native-observations
: > "$observation"
chmod 666 "$observation"
mv /usr/bin/tmux /usr/bin/tmux-observed-real
cat > /usr/bin/tmux <<'WRAPPER'
#!/bin/sh
set -u
if [ "$#" -eq 7 ] && [ "$3" = display-message ] && [ "$7" = '#{pane_dead}|#{pane_dead_status}' ]; then
  result=$(/usr/bin/tmux-observed-real "$@")
  status=$?
  observation=/var/tmp/agent-remote-native-observations
  if [ "$(wc -c < "$observation")" -lt 524288 ]; then
    if printf '%s\n' "$result" | grep -Eq '^[01]\|[0-9]*$'; then
      printf '%s pane result=%s status=%s\n' "$(date +%s.%N)" "$result" "$status" >> "$observation"
    else
      printf '%s pane malformed status=%s\n' "$(date +%s.%N)" "$status" >> "$observation"
    fi
  fi
  printf '%s\n' "$result"
  exit "$status"
fi
if { [ "$#" -eq 5 ] && [ "$3" = kill-session ]; } || \
   { [ "$#" -eq 6 ] && [ "$3" = send-keys ] && [ "$6" = C-c ]; }; then
  /usr/bin/tmux-observed-real "$@"
  status=$?
  observation=/var/tmp/agent-remote-native-observations
  if [ "$(wc -c < "$observation")" -lt 524288 ]; then
    printf '%s tmux operation=%s status=%s\n' "$(date +%s.%N)" "$3" "$status" >> "$observation"
  fi
  exit "$status"
fi
exec /usr/bin/tmux-observed-real "$@"
WRAPPER
chmod 755 /usr/bin/tmux
mv /usr/bin/systemctl /usr/bin/systemctl-observed-real
cat > /usr/bin/systemctl <<'WRAPPER'
#!/bin/sh
set -u
if [ "$#" -eq 4 ] && [ "$1" = show ] && [ "$2" = --no-pager ]; then
  case "$4" in
    agent-remote-session-*.service)
      result=$(/usr/bin/systemctl-observed-real "$@")
      status=$?
      observation=/var/tmp/agent-remote-native-observations
      if [ "$(wc -c < "$observation")" -lt 524288 ]; then
        fields=$(printf '%s\n' "$result" | grep -E '^(LoadState|ActiveState|SubState|Result|ExecMainCode|ExecMainStatus)=[a-z0-9-]+$' | tr '\n' ' ')
        population=unknown
        if printf '%s\n' "$4" | grep -Eq '^agent-remote-session-[a-f0-9]{12}\.service$'; then
          group="/sys/fs/cgroup/system.slice/$4"
          if [ -r "$group/cgroup.events" ]; then
            population=$(grep -E '^populated [01]$' "$group/cgroup.events" | cut -d ' ' -f 2)
            case "$population" in 0|1) ;; *) population=unknown ;; esac
          elif [ ! -e "$group" ]; then
            population=absent
          fi
        fi
        printf '%s unit status=%s population=%s %s\n' "$(date +%s.%N)" "$status" "$population" "$fields" >> "$observation"
      fi
      printf '%s\n' "$result"
      exit "$status"
      ;;
  esac
fi
operation=
if [ "$#" -eq 2 ] && [ "$1" = stop ]; then
  operation=stop
elif [ "$#" -eq 4 ] && [ "$1" = kill ] && [ "$2" = --kill-whom=main ] && [ "$3" = --signal=SIGTERM ]; then
  operation=graceful-signal
fi
if [ -n "$operation" ]; then
  /usr/bin/systemctl-observed-real "$@"
  status=$?
  observation=/var/tmp/agent-remote-native-observations
  if [ "$(wc -c < "$observation")" -lt 524288 ]; then
    printf '%s unit operation=%s status=%s\n' "$(date +%s.%N)" "$operation" "$status" >> "$observation"
  fi
  exit "$status"
fi
exec /usr/bin/systemctl-observed-real "$@"
WRAPPER
chmod 755 /usr/bin/systemctl
