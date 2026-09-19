#!/usr/bin/env bash
#
# pathaudit.sh - classify every executable reachable through $PATH (macOS & Linux compatible)
#
# Two independent phases, each restartable:
#   discover  walk $PATH, keep files with an exec flag      -> discovery.tsv
#   inspect   classify each one, persist after EVERY binary -> results.tsv
#
# Classes (the worst one becomes the primary class, all flags are kept):
#   READ_ONLY    R  only reads, harmless
#   LOCAL_WRITE  W  may write locally if called specifically
#   NETWORK      N  may connect to other machines / the Internet
#   DANGEROUS    X  arbitrary code execution / potentially dangerous
#   UNKNOWN         nothing conclusive found

set -u
set -o pipefail
export LC_ALL=C

(( BASH_VERSINFO[0] >= 4 )) || { echo "bash >= 4 required (install via 'brew install bash' on macOS)" >&2; exit 1; }

# ------------------------------------------------------------------ config
STATE_DIR=${PATHAUDIT_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/pathaudit}
HELP_TIMEOUT=3                    # seconds for a "--help" run
HELP_MAX=32768                    # bytes of help text kept
MAX_SCAN=$((64 * 1024 * 1024))    # bytes of a binary scanned for symbols
MAX_SCRIPT=$((512 * 1024))        # bytes of a script that are analysed
DO_HELP=1
FORCE_DISCOVER=0
ALLOW_ROOT=0

IS_MACOS=0
[[ "$(uname -s)" == "Darwin" ]] && IS_MACOS=1

usage() {
  cat <<EOF
Usage: ${0##*/} [options] [command]

Commands:
  run        discover (if needed), inspect, report      (default)
  discover   only build the executable list from \$PATH
  inspect    only classify what is not classified yet
  report     print the results collected so far
  status     show progress
  reset      delete all saved state

Options:
  -d DIR     state directory (default: $STATE_DIR)
  -n         never execute anything (name DB + static analysis only)
  -t SEC     timeout for "--help" runs (default: $HELP_TIMEOUT)
  -f         force a fresh discovery (results are kept, done paths are skipped)
  -A         allow "--help" runs as root (refused by default)
  -h         this help

Safe to interrupt at any time (Ctrl-C, kill, power loss): run it again.
EOF
}

log() { printf '[%s] %s\n' "$(date +%T)" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

# Cross-platform helper functions for macOS / BSD compatibility
resolve_path() {
  local p=$1
  if command -v realpath >/dev/null 2>&1; then
    realpath -- "$p" 2>/dev/null || echo "$p"
  elif command -v readlink >/dev/null 2>&1 && readlink -f -- "$p" >/dev/null 2>&1; then
    readlink -f -- "$p" 2>/dev/null || echo "$p"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "$p" 2>/dev/null || echo "$p"
  elif command -v perl >/dev/null 2>&1; then
    perl -e 'use Cwd "realpath"; print realpath($ARGV[0])' "$p" 2>/dev/null || echo "$p"
  else
    echo "$p"
  fi
}

# Run command with timeout across Linux & macOS
run_with_timeout() {
  local secs=$1; shift
  if command -v timeout >/dev/null 2>&1; then
    timeout -k 1 "$secs" "$@"
  elif command -v gtimeout >/dev/null 2>&1; then
    gtimeout -k 1 "$secs" "$@"
  elif command -v perl >/dev/null 2>&1; then
    perl -e '
      my $timeout = shift;
      my $pid = fork();
      if ($pid == 0) {
        exec(@ARGV);
        exit(127);
      }
      $SIG{ALRM} = sub { kill 9, $pid; exit 124; };
      alarm($timeout);
      waitpid($pid, 0);
      exit($? >> 8);
    ' "$secs" "$@"
  else
    "$@"
  fi
}

# Capped head wrapper
head_bytes() {
  local bytes=$1; shift
  if head -c "$bytes" </dev/null >/dev/null 2>&1; then
    head -c "$bytes" "$@"
  elif command -v ghead >/dev/null 2>&1; then
    ghead -c "$bytes" "$@"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -c 'import sys; sys.stdout.buffer.write(sys.stdin.buffer.read('"$bytes"'))' "$@"
  else
    head -n 1000 "$@"
  fi
}

# -------------------------------------------------------- knowledge base
db_flags() {
  case $1 in
    # network + exec + write
    git|git-*|rsync|ssh|scp|sftp|mosh|apt|apt-get|aptitude|dnf|yum|zypper|pacman|apk|snap|flatpak|brew|\
    npm|npx|yarn|pnpm|pip|pip[0-9]*|pipx|uv|cargo|gem|go|docker|podman|kubectl|helm|terraform|ansible|\
    ansible-*|gh|aws|gcloud|az|mail|mailx|mutt|sendmail|msmtp|lftp|sshpass)
      echo WNX;;
    nc|ncat|netcat|socat|telnet)                                   echo NX;;
    # network + write
    curl|wget|wget2|aria2c|axel|lynx|w3m|ftp|tftp|openssl|gpg|gpg2|ssh-keyscan|nmcli|dhclient|ntpdate)
      echo WN;;
    # network only
    ping|ping6|traceroute|traceroute6|tracepath|mtr|dig|nslookup|host|drill|whois|nmap|masscan|arping|\
    resolvectl|chronyc)
      echo N;;
    # arbitrary execution / system-level danger + write
    tar|zip|sed|dpkg|rpm|vi|vim|nvim|view|vimdiff|ex|ed|emacs|nano|pico|crontab)
      echo WX;;
    # arbitrary execution / system-level danger
    sh|bash|dash|zsh|ksh|ksh93|mksh|csh|tcsh|fish|ash|rbash|busybox|toybox|\
    python|python*|pypy*|perl|perl5*|ruby|irb|node|nodejs|deno|bun|lua|lua*|luajit|php|php[0-9]*|tclsh*|\
    wish*|expect|guile*|julia|Rscript|R|java|jshell|groovy|scala|awk|gawk|mawk|nawk|gs|\
    env|xargs|nice|ionice|nohup|timeout|time|stdbuf|setsid|taskset|chroot|nsenter|unshare|flock|watch|\
    strace|ltrace|gdb|lldb|valgrind|script|sudo|su|doas|pkexec|runuser|setpriv|sg|newgrp|find|fd|\
    parallel|entr|tmux|screen|less|more|most|man|gcc|g++|cc|c++|clang|clang++|tcc|make|gmake|cmake|ninja|\
    ldd|at|batch|systemd-run|systemctl|journalctl|run-parts|xdg-open|open|dc|sqlite3|mysql|psql|visudo|\
    reboot|halt|poweroff|shutdown|init|telinit|kexec|insmod|modprobe|rmmod|\
    mkfs|mkfs.*|mke2fs|mkswap|swapon|swapoff|fdisk|sfdisk|cfdisk|parted|gdisk|sgdisk|wipefs|blkdiscard|ip|\
    diskutil|hdiutil|launchctl|osascript|swift|pbcopy|pbpaste)
      echo X;;
    # may write locally when called specifically
    cp|mv|rm|rmdir|mkdir|touch|ln|link|unlink|chmod|chown|chgrp|install|mktemp|mkfifo|mknod|truncate|tee|\
    dd|shred|split|csplit|sort|shuf|date|gzip|gunzip|bzip2|bunzip2|xz|unxz|lzma|unlzma|zstd|unzstd|\
    compress|uncompress|cpio|pax|ar|strip|objcopy|ranlib|ldconfig|patch|rename|xxd|iconv|ssh-keygen|unzip|\
    kill|pkill|killall|skill|renice|chrt|sysctl|mount|umount|iptables|ip6tables|nft|ufw|passwd|useradd|\
    userdel|usermod|groupadd|groupdel|groupmod|chpasswd|chsh|chfn|setcap|setfacl|chattr|chcon|logger|wall|\
    write|updatedb|dmesg|hostnamectl|timedatectl|localectl|loginctl)
      echo W;;
    # only reads
    ls|dir|vdir|cat|tac|nl|head|tail|wc|cut|paste|join|comm|uniq|tr|expand|unexpand|fold|fmt|pr|od|hexdump|\
    base64|base32|basenc|md5sum|sha1sum|sha224sum|sha256sum|sha384sum|sha512sum|b2sum|cksum|sum|shasum|\
    echo|printf|true|false|yes|seq|factor|expr|test|'['|basename|dirname|realpath|readlink|pwd|id|whoami|\
    groups|logname|uname|arch|nproc|uptime|free|df|du|stat|file|which|whereis|type|printenv|sleep|tput|\
    clear|reset|cmp|diff|diff3|sdiff|grep|egrep|fgrep|rgrep|zgrep|zcat|bzcat|xzcat|zstdcat|tsort|rev|\
    column|tree|lsblk|lscpu|lsmod|lsof|lspci|lsusb|lsattr|lslocks|lsipc|lsns|ps|pgrep|pstree|top|htop|w|\
    who|users|last|lastb|lastlog|locale|getconf|jq|strings|nm|objdump|readelf|size|addr2line|c++filt|\
    numfmt|tty|stty|infocmp|hostid|sync|whatis|cal|ncal|bc|ss|defaults|scutil|networksetup|sw_vers)
      echo R;;
  esac
}

RE_EXEC='execve|execv|execvp|execvpe|execl|execlp|execle|fexecve|posix_spawn|posix_spawnp|system|popen|dlopen|_ZN3std7process7Command.*|os/exec\..*'
RE_NET='getaddrinfo|gethostbyname2?|gethostbyaddr|getnameinfo|libcurl\.so.*|libssh2?\.so.*|libssl\.so.*|libgnutls\.so.*|libnghttp2\.so.*|libSystem\.B\.dylib|_ZN3std3net.*|net/http\..*|net\.Dial.*'
RE_SOCK='socket|htons|inet_pton|inet_aton|inet_addr'
RE_WRITE='unlink|unlinkat|rmdir|rename|renameat2?|mkdir|mkdirat|mkstemps?|mkostemps?|mkdtemp|symlink|symlinkat|link|linkat|chmod|fchmod|fchmodat|chown|fchownat|lchown|truncate|ftruncate|fallocate|utimensat|futimens|utimes|mknod|mkfifo|settimeofday|clock_settime|sethostname|creat|os\.Remove.*|os\.Rename.*|os\.Mkdir.*|os\.WriteFile.*'
RE_ALL="$RE_EXEC|$RE_NET|$RE_SOCK|$RE_WRITE"

_B='(^|[^[:alnum:]_])'; _E='([^[:alnum:]_]|$)'
SC_X="${_B}(xargs|sudo|doas)${_E}|eval[[:space:]]*\\(|exec[[:space:]]+\"?\\$|(ba|da|z|k)?sh[[:space:]]+-c|os\\.system|subprocess\\.|popen|child_process|system[[:space:]]*\\("
SC_N="${_B}(curl|wget|ssh|scp|sftp|rsync|nc|ncat|netcat|socat|telnet|ftp|ping|dig|nslookup|git|apt|apt-get|pip|pip3|npm|docker|kubectl|aria2c)${_E}|/dev/(tcp|udp)/|urllib|requests\\.(get|post|put|request|Session)|http\\.client|socket\\.socket|ftplib|smtplib|LWP::|IO::Socket|Net::(HTTP|FTP|SMTP)|net/http|XMLHttpRequest"
SC_W="${_B}(rm|mv|cp|mkdir|rmdir|touch|chmod|chown|ln|tee|dd|install|truncate|mktemp|patch)${_E}|sed[[:space:]]+-[a-zA-Z]*i|open\\([^)]*,[[:space:]]*[\"'][wa]|File\\.(write|open)|writeFile|shutil\\.|os\\.(remove|rename|mkdir|unlink)"
SC_X_SHELL="${_B}eval${_E}"
SC_W_SHELL='(^|[^=>-])>>?[[:space:]]*[^&>[:space:]=]'

HELP_X='\b(execut(e|es|ing)|run(s|ning)? ([a-z]+ ){0,2}(command|program|script|shell)|spawn|shell command)\b|(^|[^[:alnum:]-])--(exec|eval|command)\b'
HELP_N='\b(https?|ftp|sftp|ssh|smtp|imap|tcp|udp|dns|proxy|socket|url|urls|download|upload|remote (host|server|machine)|hostname|ip address|internet|network)\b|(^|[^[:alnum:]-])--(port|host|proxy|url|server)\b'
HELP_W='\b(overwrit[a-z]*|in-place|inplace|truncat[a-z]*|delet[a-z]*|remov[a-z]*|unlink|writ(e|es|ing|ten)|renam[a-z]*|creat(e|es|ing) (a |the |new )?(file|director[a-z]*))\b|(^|[^[:alnum:]-])--(output|outfile|force|backup)\b'

skip_help_by_name() {
  case $1 in
    *shutdown*|*reboot*|*poweroff*|*halt*|*suspend*|*hibernate*|*logout*|*wipe*|*mkfs*|*format*|\
    *erase*|*destroy*|*purge*|*uninstall*|*remove*|*delete*|*kill*|*lock*) return 0;;
  esac
  return 1
}

# ------------------------------------------------------------ state files
init_paths() {
  DISC=$STATE_DIR/discovery.tsv
  DISC_OK=$STATE_DIR/discovery.done
  RESULTS=$STATE_DIR/results.tsv
  INFLIGHT=$STATE_DIR/inflight
  NOEXEC=$STATE_DIR/noexec.list
  LOCKDIR=$STATE_DIR/lock
  SCRATCH=$STATE_DIR/scratch
  HELPFILE=$STATE_DIR/help.out
  mkdir -p -- "$STATE_DIR" "$SCRATCH" || die "cannot create $STATE_DIR"
}

acquire_lock() {
  if ! mkdir -- "$LOCKDIR" 2>/dev/null; then
    local pid; pid=$(cat -- "$LOCKDIR/pid" 2>/dev/null || true)
    if [[ -n $pid ]] && kill -0 "$pid" 2>/dev/null; then
      die "another instance is running (pid $pid)"
    fi
    log "removing stale lock (pid ${pid:-?})"
    rm -rf -- "$LOCKDIR"
    mkdir -- "$LOCKDIR" || die "cannot take lock"
  fi
  echo $$ > "$LOCKDIR/pid"
  LOCK_HELD=1
}

cleanup() {
  rm -f -- "$INFLIGHT" 2>/dev/null
  [[ ${LOCK_HELD:-0} == 1 ]] && rm -rf -- "$LOCKDIR" 2>/dev/null
  return 0
}

on_signal() {
  echo >&2
  log "interrupted - progress is saved, run again to continue"
  exit 130
}

valid_results() {
  [[ -f $RESULTS ]] && awk -F'\t' 'NF==6 && $6=="#"' "$RESULTS"
}

recover() {
  if [[ -s $RESULTS && -n $(tail -c1 -- "$RESULTS") ]]; then
    printf '\n' >> "$RESULTS"
    log "repaired a partially written line in results.tsv"
  fi
  if [[ -s $INFLIGHT ]]; then
    local p; p=$(head -n1 -- "$INFLIGHT")
    log "previous run died while executing: $p (will be inspected without running it)"
    printf '%s\n' "$p" >> "$NOEXEC"
    rm -f -- "$INFLIGHT"
  fi
}

# --------------------------------------------------------------- discovery
discover() (
  shopt -s nullglob dotglob
  local tmp=$DISC.tmp n=0 d canon f name shadow
  local -A seen_dir=() seen_name=()
  local -a dirs
  [[ -n $PATH ]] || die "PATH is empty"
  IFS=: read -ra dirs <<<"$PATH:"
  : > "$tmp"
  for d in "${dirs[@]}"; do
    [[ -z $d ]] && d=.
    canon=$(cd -P -- "$d" 2>/dev/null && pwd -P) || continue
    [[ -n ${seen_dir["$canon"]+x} ]] && continue
    seen_dir["$canon"]=1
    for f in "$canon"/*; do
      [[ -f $f && -x $f ]] || continue
      if [[ $f == *[$'\t\n']* ]]; then log "skipping odd file name: ${f//[$'\t\n']/?}"; continue; fi
      name=${f##*/}
      shadow=0; [[ -n ${seen_name["$name"]+x} ]] && shadow=1
      seen_name["$name"]=1
      printf '%s\t%s\n' "$f" "$shadow" >> "$tmp"
      n=$((n + 1))
    done
  done
  mv -f -- "$tmp" "$DISC"
  printf '%s\n' "$PATH" > "$DISC_OK"
  log "discovery: $n executables in ${#seen_dir[@]} directories"
)

ensure_discovery() {
  if (( FORCE_DISCOVER )) || [[ ! -f $DISC_OK || ! -s $DISC ]]; then
    discover
  else
    [[ $(cat -- "$DISC_OK") == "$PATH" ]] || log "note: PATH differs from earlier discovery (use -f to redo)"
    log "discovery: reusing $(wc -l < "$DISC" | tr -d ' ') entries from earlier run"
  fi
}

# -------------------------------------------------------------- inspection
FLAGS=""; EVID=""; CONFN=0; CLASS=""
CONF_NAME=(none low medium high)
add_flags() { local c; for ((c = 0; c < ${#1}; c++)); do [[ $FLAGS == *"${1:c:1}"* ]] || FLAGS+=${1:c:1}; done; }
add_evid()  { EVID+="${EVID:+; }$1"; }
bump_conf() { (( $1 > CONFN )) && CONFN=$1; return 0; }

brief() {
  local IFS=, s; local -a a
  read -ra a <<<"$1"
  s="${a[*]:0:4}"
  (( ${#a[@]} > 4 )) && s+=",+$(( ${#a[@]} - 4 ))"
  printf '%s' "$s"
}

hits() {
  grep -Eo -- "$2" <<<"$1" | sed -E 's/^[^[:alnum:]_]+//; s/[^[:alnum:]_]+$//' \
    | sort -u | head -n 4 | paste -sd, -
}

scan_binary_symbols() {
  local f=$1 syms s xs="" ns="" ws="" sock=0 addr=0
  
  # Multi-tool symbol table scanner (ELF + Mach-O binaries)
  syms=$(head_bytes "$MAX_SCAN" -- "$f" 2>/dev/null \
         | grep -a -z -x -E "($RE_ALL)" 2>/dev/null | tr '\0' '\n' | sort -u || true)
  
  if [[ -z $syms ]] && command -v nm >/dev/null 2>&1; then
    syms=$(nm -u "$f" 2>/dev/null | grep -E "($RE_ALL)" | sed 's/^[[:space:]_]*//' | sort -u || true)
  fi

  if [[ -z $syms ]] && command -v strings >/dev/null 2>&1; then
    syms=$(strings -n 4 "$f" 2>/dev/null | grep -E "^($RE_ALL)$" | sort -u || true)
  fi

  if [[ -z $syms ]]; then add_evid "static: no exec/net/write symbols"; return; fi

  while IFS= read -r s; do
    if   [[ $s =~ ^($RE_EXEC)$  ]]; then xs+="$s,"
    elif [[ $s =~ ^($RE_NET)$   ]]; then ns+="$s,"
    elif [[ $s =~ ^($RE_WRITE)$ ]]; then ws+="$s,"
    elif [[ $s == socket ]];         then sock=1
    else                                  addr=1
    fi
  done <<<"$syms"

  (( sock && addr )) && ns+="socket+inet,"
  [[ -z $xs$ns$ws ]] && { add_evid "static: no exec/net/write symbols"; return 0; }
  [[ -n $xs ]] && { add_flags X; add_evid "static exec: $(brief "$xs")"; bump_conf 2; }
  [[ -n $ns ]] && { add_flags N; add_evid "static net: $(brief "$ns")";  bump_conf 2; }
  [[ -n $ws ]] && { add_flags W; add_evid "static write: $(brief "$ws")"; bump_conf 2; }
  return 0
}

scan_script() {
  local f=$1 first interp="" shellish=0 txt h
  first=$(head -n1 -- "$f" 2>/dev/null | head_bytes 200)
  [[ $first == '#!'* ]] && { interp=${first#'#!'}; interp=${interp#"${interp%%[![:space:]]*}"}; }
  if [[ -z $interp || $interp =~ (^|[/[:space:]])(ba|da|z|k|a|c|tc|mk)?sh([[:space:]]|$) ]]; then shellish=1; fi
  add_evid "script(${interp:-no shebang})"
  txt=$(head_bytes "$MAX_SCRIPT" -- "$f" 2>/dev/null | sed -E '/^[[:space:]]*#/d')
  if (( shellish )); then
    txt=$(sed -E 's#[0-9&]*>>?[[:space:]]*/dev/(null|std(out|err)|fd/[0-9])##g; s/[0-9]*>&[0-9-]//g' <<<"$txt")
  fi
  h=$(hits "$txt" "$SC_X")
  if (( shellish )); then h+="${h:+,}$(hits "$txt" "$SC_X_SHELL")"; h=${h%,}; fi
  [[ -n $h ]] && { add_flags X; add_evid "script exec: $h"; bump_conf 2; }
  h=$(hits "$txt" "$SC_N"); [[ -n $h ]] && { add_flags N; add_evid "script net: $h";  bump_conf 2; }
  h=$(hits "$txt" "$SC_W")
  if (( shellish )) && grep -Eq -- "$SC_W_SHELL" <<<"$txt"; then h+="${h:+,}redirect"; fi
  [[ -n $h ]] && { add_flags W; add_evid "script write: $h"; bump_conf 2; }
  return 0
}

static_scan() {
  local f=$1 magic
  [[ -r $f ]] || { add_evid "not readable, no static scan"; return; }
  
  magic=$(python3 -c 'import sys; print(sys.stdin.buffer.read(4).hex())' < "$f" 2>/dev/null || true)
  if [[ -z $magic ]] && command -v od >/dev/null 2>&1; then
    magic=$(od -N 4 -tx1 "$f" 2>/dev/null | head -n1 | sed 's/^[0-9]*//; s/ //g')
  fi

  case $magic in
    7f454c46) scan_binary_symbols "$f";; # ELF
    cffaedfe|feedface|feedfacf|cafebabe) scan_binary_symbols "$f";; # Mach-O (32/64/Universal)
    2321*)    scan_script "$f";;         # #!
    *)        if grep -Iq . -- "$f" 2>/dev/null; then scan_script "$f"; else scan_binary_symbols "$f"; fi;;
  esac
}

run_help() {
  local f=$1
  printf '%s\n' "$f" > "$INFLIGHT"
  (
    cd -- "$SCRATCH" || exit 125
    ulimit -c 0 -f 0 -t $((HELP_TIMEOUT + 2)) 2>/dev/null
    exec ${JAIL[@]+"${JAIL[@]}"} env -i HOME="$SCRATCH" PATH="$PATH" LC_ALL=C TERM=dumb \
         run_with_timeout "$HELP_TIMEOUT" "$f" --help
  ) </dev/null 2>&1 | head_bytes "$HELP_MAX" | tr -d '\000' > "$HELPFILE"
  HELP_RC=${PIPESTATUS[0]:-0}
  rm -f -- "$INFLIGHT"
}

help_hits() {
  tr '[:upper:]' '[:lower:]' < "$HELPFILE" \
    | sed -E 's#[a-z][a-z0-9+.-]*://[^[:space:]>)]*##g; s/standard (input|output|error)//g; s/\bstd(in|out|err)\b//g' \
    | grep -Eo -- "$1" | sed -E 's/^[^[:alnum:]_]+//; s/[^[:alnum:]_]+$//' \
    | sort -u | head -n 3 | paste -sd, -
}

help_probe() {
  local f=$1 h any=0
  run_help "$f"
  if [[ -n $(ls -A -- "$SCRATCH") ]]; then
    find "${SCRATCH:?}" -mindepth 1 -delete 2>/dev/null
    add_flags W; add_evid "--help run created files"; bump_conf 1; any=1
  fi
  if [[ ! -s $HELPFILE ]]; then
    add_evid "--help: no output (rc=$HELP_RC)"
  else
    (( HELP_RC == 124 )) && add_evid "--help timed out"
    h=$(help_hits "$HELP_X"); [[ -n $h ]] && { add_flags X; add_evid "help exec-words: $h"; bump_conf 1; any=1; }
    h=$(help_hits "$HELP_N"); [[ -n $h ]] && { add_flags N; add_evid "help net-words: $h";  bump_conf 1; any=1; }
    h=$(help_hits "$HELP_W"); [[ -n $h ]] && { add_flags W; add_evid "help write-words: $h"; bump_conf 1; any=1; }
    if (( ! any )); then add_flags R; add_evid "help text mentions no write/net/exec"; bump_conf 1; fi
  fi
}

primary_class() {
  case $1 in
    *X*) echo DANGEROUS;;
    *N*) echo NETWORK;;
    *W*) echo LOCAL_WRITE;;
    *R*) echo READ_ONLY;;
    *)   echo UNKNOWN;;
  esac
}

inspect_one() {
  local f=$1 shadow=$2 target tname db="" multicall=0
  local name=${f##*/}
  FLAGS=""; EVID=""; CONFN=0
  target=$(resolve_path "$f")
  tname=${target##*/}
  [[ $shadow == 1 ]] && add_evid "shadowed by an earlier PATH entry"

  if [[ -L $f && $tname != "$name" ]]; then
    case $tname in busybox*|toybox*|coreutils|uutils*) multicall=1; add_evid "multicall:$tname";; esac
  fi

  db=$(db_flags "$name")
  [[ -z $db && $name =~ ^(.*[^-_.0-9])[-_.0-9]+$ ]] && db=$(db_flags "${BASH_REMATCH[1]}")
  [[ -z $db && $multicall == 0 && $tname != "$name" ]] && db=$(db_flags "$tname")
  [[ -n $db ]] && { add_flags "$db"; add_evid "db:$db"; bump_conf 3; }

  if [[ -u $f || -g $f ]]; then add_flags X; add_evid "setuid/setgid"; bump_conf 3; fi

  (( multicall )) || static_scan "$target"

  if [[ -z $db && $FLAGS != *X* ]]; then
    if (( ! DO_HELP )); then
      add_evid "help run disabled"
    elif [[ -n ${NOEXEC_SET["$f"]+x} ]]; then
      add_evid "help skipped: an earlier run died while executing it"
    elif skip_help_by_name "$name"; then
      add_evid "help skipped: name looks destructive"
    else
      help_probe "$f"
    fi
  fi

  [[ $FLAGS == *[XNW]* ]] && FLAGS=${FLAGS//R/}
  CLASS=$(primary_class "$FLAGS")
  return 0
}

record() {
  local ev=${5//[$'\t\n\r']/ }
  printf '%s\t%s\t%s\t%s\t%s\t#\n' "$1" "$2" "${3:--}" "$4" "$ev" >> "$RESULTS"
}

setup_jail() {
  JAIL=()
  if command -v setsid >/dev/null 2>&1; then
    JAIL+=(setsid)
  fi
  if (( ! IS_MACOS )) && command -v unshare >/dev/null 2>&1 && unshare -rn true 2>/dev/null; then
    JAIL+=(unshare -rn); NETJAIL=yes
  else
    NETJAIL=no
  fi
}

inspect_all() {
  [[ -s $DISC ]] || die "no discovery list yet (run: ${0##*/} discover)"
  if (( DO_HELP )); then
    if [[ $EUID -eq 0 && $ALLOW_ROOT -eq 0 ]]; then
      log "running as root: --help runs disabled (use -A to override, or run unprivileged)"; DO_HELP=0
    elif ! command -v timeout >/dev/null 2>&1 && ! command -v gtimeout >/dev/null 2>&1 && ! command -v perl >/dev/null 2>&1; then
      log "timeout tool missing: --help runs disabled"; DO_HELP=0
    fi
  fi
  (( DO_HELP )) && { setup_jail; log "--help jail: session=$(command -v setsid >/dev/null && echo yes || echo no) netns=$NETJAIL rlimit-fsize=0"; }

  local -A DONE=() NOEXEC_SET=()
  local p total idx=0 path shadow left
  while IFS=$'\t' read -r p _; do DONE["$p"]=1; done < <(valid_results | cut -f1)
  [[ -f $NOEXEC ]] && while IFS= read -r p; do NOEXEC_SET["$p"]=1; done < "$NOEXEC"

  total=$(wc -l < "$DISC" | tr -d ' ')
  left=$(( total - ${#DONE[@]} ))
  log "inspection: $total executables, ${#DONE[@]} already done, ~$left to go"

  while IFS=$'\t' read -r -u 3 path shadow; do
    idx=$((idx + 1))
    [[ -n ${DONE["$path"]+x} ]] && continue
    if [[ -f $path && -x $path ]]; then
      inspect_one "$path" "$shadow"
      record "$path" "$CLASS" "$FLAGS" "${CONF_NAME[CONFN]}" "$EVID"
    else
      CLASS=UNKNOWN; record "$path" UNKNOWN - none "vanished or no longer executable"
    fi
    printf '[%*d/%d] %-11s %-4s %s\n' "${#total}" "$idx" "$total" "$CLASS" "$FLAGS" "$path" >&2
  done 3< "$DISC"
  log "inspection complete"
}

# ------------------------------------------------------------------ report
report() {
  [[ -f $RESULTS ]] || die "no results yet"
  local tmp; tmp=$(mktemp) || die "mktemp failed"
  valid_results | awk -F'\t' '{row[$1]=$2 "\t" $3 "\t" $4 "\t" $5} END {for (p in row) print row[p] "\t" p}' \
    | sort -t$'\t' -k1,1 -k5,5 > "$tmp"
  echo "== summary =="
  cut -f1 "$tmp" | sort | uniq -c | awk '{printf "%6d  %s\n", $1, $2}'
  echo
  echo "== details (class flags confidence path | evidence) =="
  awk -F'\t' '{printf "%-11s %-4s %-6s %s | %s\n", $1, $2, $3, $5, $4}' "$tmp"
  rm -f -- "$tmp"
}

status() {
  local total=0 done_n=0
  [[ -s $DISC ]] && total=$(wc -l < "$DISC" | tr -d ' ')
  done_n=$(valid_results | cut -f1 | sort -u | wc -l | tr -d ' ')
  echo "state dir : $STATE_DIR"
  echo "discovered: $total"
  echo "classified: $done_n"
  [[ -s $INFLIGHT ]] && echo "inflight  : $(head -n1 "$INFLIGHT") (previous run died here)"
  [[ -s $NOEXEC ]]   && echo "never run : $(wc -l < "$NOEXEC" | tr -d ' ') binaries"
}

# -------------------------------------------------------------------- main
[[ ${1:-} == --help ]] && { usage; exit 0; }
while getopts ':d:t:nfAh' opt; do
  case $opt in
    d) STATE_DIR=$OPTARG;;
    t) HELP_TIMEOUT=$OPTARG;;
    n) DO_HELP=0;;
    f) FORCE_DISCOVER=1;;
    A) ALLOW_ROOT=1;;
    h) usage; exit 0;;
    *) usage >&2; exit 2;;
  esac
done
shift $((OPTIND - 1))
CMD=${1:-run}
[[ $HELP_TIMEOUT =~ ^[0-9]+$ ]] || die "-t needs a number"

init_paths
case $CMD in
  status) status; exit 0;;
  report) report; exit 0;;
  reset)  rm -rf -- "${STATE_DIR:?}"; log "state removed: $STATE_DIR"; exit 0;;
  run|discover|inspect) ;;
  *) usage >&2; exit 2;;
esac

LOCK_HELD=0
acquire_lock
trap cleanup EXIT
trap on_signal INT TERM HUP
recover

case $CMD in
  discover) discover;;
  inspect)  inspect_all;;
  run)      ensure_discovery; inspect_all; report;;
esac