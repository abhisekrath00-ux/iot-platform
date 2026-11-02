#!/usr/bin/env python3
"""Read-only HTTP checkpoint for generated HA listeners, not a failover drill.

Run only against infrastructure you are authorized to inspect. No credentials,
redirects, retries, environment proxies or mutation requests. HTTP only: generated
listeners have no TLS termination. This does not certify a secure deployment.
"""
import argparse
import concurrent.futures
import http.client
import ipaddress
import json
import socket
import threading
import time


def node_list(value):
    nodes = []
    for item in value.split(','):
        name, sep, ip = item.partition('=')
        if not sep or not name or not name[0].islower() or len(name) > 31 or any(c not in 'abcdefghijklmnopqrstuvwxyz0123456789-' for c in name):
            raise argparse.ArgumentTypeError('nodes require three distinct lowercase name=IPv4 entries')
        try:
            ip = str(ipaddress.IPv4Address(ip))
        except ValueError:
            raise argparse.ArgumentTypeError('node address must be IPv4') from None
        nodes.append((name, ip))
    if len(nodes) != 3 or len({n for n, _ in nodes}) != 3 or len({ip for _, ip in nodes}) != 3:
        raise argparse.ArgumentTypeError('exactly three distinct node names and addresses required')
    return nodes


def targets(nodes, vip):
    out = []
    for name, ip in nodes:
        out.extend([(name + '/api', ip, 8001, '/readyz', 'ready'),
                    (name + '/web', ip, 8081, '/readyz', 'ready'),
                    (name + '/primary', ip, 8008, '/primary', 'primary')])
    out.extend([('vip/api', vip, 8000, '/readyz', 'ready'),
                ('vip/web', vip, 8080, '/readyz', 'ready')])
    return out


def probe(target, timeout):
    name, host, port, path, kind = target
    started = time.monotonic()
    result = dict(target=name, kind=kind, status=None, outcome='unavailable')
    conn = http.client.HTTPConnection(host, port, timeout=timeout)
    timer = None
    try:
        conn.request('GET', path, headers={'Connection': 'close'})
        remaining = timeout - (time.monotonic() - started)
        if remaining <= 0:
            raise TimeoutError()
        # A per-socket timeout resets on each read. Close the captured socket at
        # the overall deadline too, so a peer trickling headers cannot extend it.
        sock = conn.sock
        def expire():
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        timer = threading.Timer(remaining, expire)
        timer.start()
        response = conn.getresponse()
        # Status only. Never record a response body, headers or exception text:
        # upstream output may contain secrets and an oversized body is irrelevant.
        result['status'] = response.status
        if response.status == 200:
            result['outcome'] = 'primary' if kind == 'primary' else 'ready'
        elif kind == 'primary' and response.status == 503:
            result['outcome'] = 'not_primary'
        else:
            result['outcome'] = 'unexpected_status'
    except (OSError, http.client.HTTPException):
        pass
    finally:
        if timer is not None:
            timer.cancel()
        conn.close()
    result['elapsed_ms'] = round((time.monotonic() - started) * 1000)
    return result


def check(selected, timeout=2.0):
    if not 0 < timeout <= 10:
        raise ValueError('timeout must be greater than zero and at most ten seconds')
    with concurrent.futures.ThreadPoolExecutor(max_workers=11) as executor:
        results = list(executor.map(lambda target: probe(target, timeout), selected))
    ready = all(r['outcome'] == 'ready' for r in results if r['kind'] == 'ready')
    primaries = [r['target'] for r in results if r['outcome'] == 'primary']
    known = all(r['outcome'] in ('primary', 'not_primary') for r in results if r['kind'] == 'primary')
    ok = ready and known and len(primaries) == 1
    return dict(checkpoint_passed=ok, primary_targets=primaries, results=results,
                scope='One HTTP sample per target, not an atomic cluster view or failover/TLS/replication/capacity proof.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--nodes', type=node_list, required=True)
    parser.add_argument('--vip', type=ipaddress.IPv4Address, required=True)
    parser.add_argument('--timeout', type=float, default=2.0)
    args = parser.parse_args()
    if not 0 < args.timeout <= 10:
        parser.error('timeout must be greater than zero and at most ten seconds')
    vip = str(args.vip)
    if vip in {ip for _, ip in args.nodes}:
        parser.error('VIP must differ from node addresses')
    result = check(targets(args.nodes, vip), args.timeout)
    print(json.dumps(result, indent=2))
    return 0 if result['checkpoint_passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
