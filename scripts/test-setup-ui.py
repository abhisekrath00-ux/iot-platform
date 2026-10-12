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
  "compose ps") case "$*" in *"--format {{.State}}") if [ -n "${HX_ALL_UP:-}" ]; then printf 'running\\nrunning\\n'; else printf 'running\\nexited\\n'; fi ;; *) if [ -n "${HX_ALL_UP:-}" ]; then printf 'api|running|healthy\\nweb|running|\\npostgres|running|healthy\\n'; else printf 'api|running|healthy\\nweb|running|\\npostgres|running|healthy\\ningest|exited|\\n'; fi ;; esac ;;
  "compose exec") exit 0 ;;
  "compose up"|"compose restart") echo "stub: $*" >> "$HX_STUB_LOG" ;;
  "compose --profile") echo "stub: $*" >> "$HX_STUB_LOG"; case "$*" in *"ps --status"*) [ -n "${HX_AI_UP:-}" ] && echo ai-runtime ;; esac ;;
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
# ---- Ubuntu/Linux terminal command: start/stop/restart/doctor/ai/open/version and the `hexthings` link ----
def stublog():
    p = os.path.join(T, "stub.log"); return open(p).read() if os.path.exists(p) else ""
def clearlog():
    p = os.path.join(T, "stub.log")
    if os.path.exists(p): os.remove(p)
clearlog(); o, rc = run(["start"]); ok("start runs compose up -d", rc == 0 and "stub: compose up -d" in stublog() and "Started" in o, o + stublog())
clearlog(); o, rc = run(["stop"]); ok("stop runs compose stop and says data is kept", rc == 0 and "stub: compose stop" in stublog() and "Data is kept" in o, o + stublog())
clearlog(); o, rc = run(["restart"]); ok("restart runs compose restart", rc == 0 and "stub: compose restart" in stublog(), o + stublog())
o, rc = run(["doctor"], e=env(HEXTHINGS_MQTT_PORT=MQ, HX_ALL_UP="1")); ok("doctor: everything up -> exit 0 and says nothing wrong", rc == 0 and "docker engine answers" in o and "Doctor found nothing wrong" in o, o)
o, rc = run(["doctor"], e=env(HEXTHINGS_MQTT_PORT=MQ)); ok("doctor: a service down -> exit 1 and says so", rc == 1 and "at least one service is not running" in o and "Doctor found problems" in o, o)
o, rc = run(["doctor"], e=env(HEXTHINGS_MQTT_PORT=MQ, HX_ALL_UP="1", HX_API_DOWN="1")); ok("doctor: API down -> exit 1", rc == 1 and "Doctor found problems" in o, o)
o, rc = run(["ai"]); ok("ai status without a model explains how to add one", rc == 0 and "models/model.gguf is missing" in o, o)
os.makedirs(os.path.join(ROOT, "models"), exist_ok=True); mg = os.path.join(ROOT, "models", "model.gguf"); had = os.path.exists(mg)
if not had: open(mg, "wb").write(b"x")
try:
    o, rc = run(["ai", "status"]); ok("ai status: model installed but not running", "NOT running" in o, o)
    o, rc = run(["ai", "status"], e=env(HX_AI_UP="1")); ok("ai status: running", "is running" in o and "NOT" not in o, o)
    clearlog(); o, rc = run(["ai", "start"]); ok("ai start recreates ai-runtime only", "up -d --force-recreate ai-runtime" in stublog(), o + stublog())
    clearlog(); o, rc = run(["ai", "stop"]); ok("ai stop stops ai-runtime only", "stop ai-runtime" in stublog(), o + stublog())
    clearlog(); o, rc = run(["start"]); ok("start includes the ai profile when a model is installed", "--profile ai up -d" in stublog(), stublog())
finally:
    if not had: os.remove(mg)
o, rc = run(["ai", "bogus"]); ok("ai with a bad word: usage, exit 2", rc == 2 and "usage: ai" in o, o)
o, rc = run(["version"]); ok("version prints something", rc == 0 and "HexThings" in o, o)
o, rc = run(["open"]); ok("open prints the web address", "localhost:8080" in o, o)
os.chmod(os.path.join(ROOT, "scripts", "hexthings"), 0o755)  # the web editor does not keep the exec bit
lk = os.path.join(T, "linkdir"); os.makedirs(lk); os.symlink(os.path.join(ROOT, "scripts", "hexthings"), os.path.join(lk, "hexthings"))
r = subprocess.run([os.path.join(lk, "hexthings"), "status"], cwd="/", env=env(), capture_output=True, text=True)
ok("hexthings via a symlink works from any directory", r.returncode == 0 and "services up" in r.stdout, r.stdout + r.stderr)
r = subprocess.run(["bash", os.path.join(ROOT, "scripts", "hexthings"), "help"], cwd="/", env=env(), capture_output=True, text=True)
ok("hexthings help prints usage", r.returncode == 0 and "hexthings ai" in r.stdout, r.stdout)
o, rc = tty([], [b"j" * 9 + b"\r", b"q"], total=12)
ok("menu: arrow to the 10th entry (Start) runs it", "Started" in o and "Press Enter to return" in o, o[-300:])
o, rc = tty([], [b"q"]); ok("menu lists Doctor, AI status and Open", all(w in o for w in ("Doctor", "AI status", "Open in browser")), o[-400:])
sys.exit(1 if fails else 0)
