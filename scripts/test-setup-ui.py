#!/usr/bin/env python3
"""Tests scripts/setup.sh (the terminal app) through a real pseudo-terminal, with fake docker/curl on PATH.
Run: python3 scripts/test-setup-ui.py. It proves menu drawing, keys, Ctrl-C, narrow/no-colour/non-TTY handling,
bad input and the status/health/cluster/restore logic against STUBBED docker and curl. It does not prove
anything about a real Docker, real services or a real Patroni cluster."""
import os, re, socket, pty, select, signal, subprocess, sys, tempfile, time
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
T = tempfile.mkdtemp()
BIN = os.path.join(T, "bin"); os.makedirs(BIN)
def stub(name, body):
    p = os.path.join(BIN, name); open(p, "w").write("#!/usr/bin/env bash\n" + body); os.chmod(p, 0o755)
stub("docker", '''
case "$1 $2" in
  "info "*) exit 0 ;;
  "compose ps") printf 'api|running|healthy\\nweb|running|\\npostgres|running|healthy\\ningest|exited|\\n' ;;
  "compose exec") exit 0 ;;
  "compose stop"|"compose start") echo "stub: $*" >> "$HX_STUB_LOG" ;;
  "compose logs") echo "stub log line"; sleep 30 ;;
esac
exit 0''')
stub("curl", '''
for a in "$@"; do u="$a"; done
case "$u" in
  *:8008/cluster) [ -n "${HX_NO_CLUSTER:-}" ] && exit 7; echo '{"members":[{"name":"n1","role":"leader","state":"running","lag":0},{"name":"n2","role":"sync_standby","state":"streaming","lag":0},{"name":"n3","role":"replica","state":"stopped","lag":"unknown"}]}' ;;
  *:8000/healthz) [ -n "${HX_API_DOWN:-}" ] && { printf '503 0.002'; exit 0; }; printf '200 0.012' ;;
  *) printf '200 0.034' ;;
esac''')
fails = 0
def ok(name, cond, extra=""):
    global fails
    print(("ok   " if cond else "FAIL ") + name + ("" if cond else "  " + extra)); fails += 0 if cond else 1
def env(**kw):
    e = dict(os.environ, PATH=BIN + ":" + os.environ["PATH"], TERM="xterm-256color", LANG="en_US.UTF-8", COLUMNS="100", HX_STUB_LOG=os.path.join(T, "stub.log"))
    e.pop("NO_COLOR", None); e.update(kw); return e
def tty(args, keys, e=None, wait=1.2, total=15, ctrlc_after=None):
    pid, fd = pty.fork()
    if pid == 0:
        os.chdir(ROOT); os.execvpe("bash", ["bash", "scripts/setup.sh"] + args, e or env())
    out = b""; t0 = time.time(); ki = 0; last = time.time(); cc = False
    while time.time() - t0 < total:
        r, _, _ = select.select([fd], [], [], 0.2)
        if r:
            try: d = os.read(fd, 65536)
            except OSError: break
            if not d: break
            out += d; last = time.time()
        elif time.time() - last > wait and ki < len(keys):
            os.write(fd, keys[ki]); ki += 1; last = time.time()
        elif ki >= len(keys) and ctrlc_after and not cc and time.time() - last > ctrlc_after:
            os.write(fd, b"\x03"); cc = True; last = time.time()
        elif ki >= len(keys) and time.time() - last > 3 and not ctrlc_after:
            break
    try:
        _, st = os.waitpid(pid, os.WNOHANG)
        if st == 0:
            try: os.kill(pid, signal.SIGKILL)
            except Exception: pass
            _, st = os.waitpid(pid, 0)
    except ChildProcessError: st = 0
    return out.decode("utf-8", "replace"), (os.WEXITSTATUS(st) if os.WIFEXITED(st) else 128 + os.WTERMSIG(st))
def run(args, e=None, stdin=subprocess.DEVNULL):
    p = subprocess.run(["bash", "scripts/setup.sh"] + args, cwd=ROOT, env=e or env(), capture_output=True, text=True, stdin=stdin)
    return p.stdout + p.stderr, p.returncode

o, rc = tty([], [b"q"])
ok("menu draws gradient wordmark (block glyphs + 256-colour)", o.count("█") > 50 and "\x1b[38;5;" in o)
ok("menu lists all actions", all(x in o for x in ("Install single node", "multi node", "Status", "Health check", "Follow logs", "Backup now", "Restore a backup", "Upgrade", "Cluster view", "Quit")))
ok("q quits cleanly, cursor restored", rc == 0 and "\x1b[?25h" in o, f"rc={rc}")
o, rc = tty([], [b"\x1b[B", b"\x1b[B", b"\r", b"\r", b"q"])
ok("arrow keys then Enter run Status (stubbed docker)", "4 of 5 services up" in o or "3 of 4 services up" in o, o[-400:])
ok("status shows a failed service as down", "ingest" in o and "exited" in o)
o, rc = tty([], [b"4", b"\r", b"q"])
ok("number key 4 runs Health with latency", "healthy" in o and "ms" in o, o[-300:])
o, rc = tty([], [b"z", b"\x1b[Z", b"@", b"q"])
ok("unknown keys are ignored without crashing", rc == 0)
o, rc = tty([], [], ctrlc_after=1.0)
ok("Ctrl-C leaves with 130 and restores the cursor", rc == 130 and "\x1b[?25h" in o, f"rc={rc}")
o, rc = tty([], [b"5", b"\r", b"\x03"], wait=1.0, total=12, ctrlc_after=1.5)
ok("Ctrl-C during logs returns to the menu (does not kill the app)", "stub log line" in o and "Press Enter to return" in o, o[-300:])
o, rc = tty([], [b"q"], e=env(COLUMNS="40"))
ok("narrow terminal: plain wordmark, no overflow", "HexThings" in o and o.count("█") == 0 and max(len(re.sub(r"\x1b\[[0-9;?]*[A-Za-z]", "", l)) for l in o.replace("\r", "").split("\n")) <= 40)
o, rc = tty([], [b"x\n", b"99\n", b"\n", b"q\n"], e=env(NO_COLOR="1"))
ok("NO_COLOR: numbered plain menu, no colour escapes", "1. Install single node" in o and "\x1b[38;5" not in o and "\x1b[2J" not in o)
ok("NO_COLOR: bad input is rejected, then q quits", "'x' is not a choice" in o and "'99' is not a choice" in o and rc == 0, f"rc={rc}")
o, rc = run([])
ok("non-TTY without a command: usage and exit 2", rc == 2 and "needs a terminal" in o)
o, rc = run(["bogus"]); ok("unknown command rejected with usage (exit 2)", rc == 2 and "unknown command" in o)
srv = socket.socket(); srv.bind(("127.0.0.1", 0)); srv.listen(5); MQ = str(srv.getsockname()[1])
o, rc = run(["health"], e=env(HEXTHINGS_MQTT_PORT=MQ)); ok("health command: all up -> exit 0", rc == 0 and "All probed services answer" in o, o)
o, rc = run(["health"], e=env(HX_API_DOWN="1", HEXTHINGS_MQTT_PORT=MQ)); ok("health command: API down -> exit 1 and says DOWN", rc == 1 and "DOWN" in o and "http 503" in o, o)
o, rc = run(["status"]); ok("status command works non-interactively", rc == 0 and "services up" in o, o)
o, rc = run(["cluster", "n1"]); ok("cluster view shows leader, sync standby, stopped replica", "LEADER" in o and "SYNC" in o and "stopped" in o and "3 member" in o, o)
o, rc = run(["cluster", "n1"], e=env(HX_NO_CLUSTER="1")); ok("cluster view with no answer explains itself, exit 1", rc == 1 and "No answer from Patroni" in o, o)
o, rc = run(["restore"]); ok("restore without a file refuses (exit 2)", rc == 2)
o, rc = run(["restore", "/nonexistent"]); ok("restore with a missing file refuses", rc == 2)
fake = os.path.join(T, "b.dump.gz"); open(fake, "wb").write(b"x")
o, rc = subprocess.run(["bash", "-c", f"echo nope | bash scripts/setup.sh restore {fake}"], cwd=ROOT, env=env(), capture_output=True, text=True).stdout, 0
ok("restore: wrong confirmation changes nothing (no stop/start issued)", not os.path.exists(os.path.join(T, "stub.log")), o)
o, rc = run(["multi"]); ok("multi without args off-terminal prints usage, exit 0", rc == 0 and "multi node:" in o)
o, rc = run(["multi", "plan", "--nodes", "a=10.0.0.11,b=10.0.0.12,c=10.0.0.13", "--vip", "10.0.0.10"]); ok("multi plan still reaches the HA generator", "any ONE node" in o, o)
sys.exit(1 if fails else 0)
