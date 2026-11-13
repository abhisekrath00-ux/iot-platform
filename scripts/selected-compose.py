#!/usr/bin/env python3
"""Route operations through verified Compose. No selector creation or lab fallback.
Pending/invalid state blocks before Docker. The OS lock spans the Docker call.
"""
import importlib.util
import pathlib
import subprocess
import sys

spec = importlib.util.spec_from_file_location('state', pathlib.Path(__file__).with_name('secure-state.py'))
state = importlib.util.module_from_spec(spec)
spec.loader.exec_module(state)


def command(root, args):
    selected = state.selection(root)
    if selected:
        return ['docker', 'compose', '-p', selected['project'], '-f', selected['config'], *args]
    return ['docker', 'compose', *args]


if __name__ == '__main__':
    if len(sys.argv) < 3:
        raise SystemExit('usage: selected-compose.py ROOT compose-args...')
    try:
        root = pathlib.Path(sys.argv[1]).resolve()
        directory = root / '.secure-deployment'
        if directory.exists() or directory.is_symlink():
            with state.Journal(root):
                raise SystemExit(subprocess.call(command(root, sys.argv[2:]), cwd=root))
        raise SystemExit(subprocess.call(command(root, sys.argv[2:]), cwd=root))
    except (ValueError, OSError, KeyError, TypeError) as error:
        print(str(error), file=sys.stderr)
        raise SystemExit(1)
