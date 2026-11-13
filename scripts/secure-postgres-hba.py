#!/usr/bin/env python3
"""Recoverable PostgreSQL host-auth barrier, not a complete host provider.

Preserves the original first-match authentication for the explicit local admin.
Network access stays denied except exact reviewed application host/user pairs,
using SCRAM and the same SSL class as their original first-match rule. No trust,
new role/password, hostname/DNS permission, or general localhost bypass is added.
The caller still owns the deployment lock, scope, actual Docker/volume/peer
inventory, reload verification and fresh positive/negative connection proofs.
"""
import hashlib
import importlib.util
import ipaddress
import os
import pathlib
import re
import stat
import tempfile

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('hba_state', HERE / 'secure-state.py')
state = importlib.util.module_from_spec(spec); spec.loader.exec_module(state)
NAME = re.compile(r'[a-zA-Z_][a-zA-Z0-9_]{0,62}')


def digest(data):
    return hashlib.sha256(data).hexdigest()


def name(value):
    if not isinstance(value, str) or not NAME.fullmatch(value) or value in {'all', 'replication', 'sameuser', 'samerole'}:
        raise ValueError('Exact simple non-special PostgreSQL name required for this barrier.')
    return value


def rules(data):
    if not isinstance(data, bytes) or len(data) > 1024 * 1024:
        raise ValueError('Bounded exact original host-auth bytes required.')
    try: text = data.decode('utf-8')
    except UnicodeError: raise ValueError('Host-auth encoding requires separate support.') from None
    found = []
    for raw in text.splitlines():
        fields = raw.split('#', 1)[0].split()
        if not fields: continue
        if any(char in raw.split('#', 1)[0] for char in ['"', "'", '\\', ',', '+', '@', '\x00']):
            raise ValueError('Quoted/continued/group/include host-auth rules require separate support.')
        kind = fields[0]
        if kind not in {'local', 'host', 'hostssl', 'hostnossl'} or len(fields) != (4 if kind == 'local' else 5):
            raise ValueError('Unsupported host-auth rule or options; preserve without changes.')
        for field in fields[1:3]:
            if not NAME.fullmatch(field):
                raise ValueError('Unsupported host-auth database/user selector.')
        method = fields[-1]
        if method not in {'trust', 'peer', 'reject', 'scram-sha-256', 'md5'} or (method == 'peer' and kind != 'local'):
            raise ValueError('Unsupported host-auth method; preserve without changes.')
        network = None
        if kind != 'local':
            if fields[3] != 'all':
                try: network = ipaddress.ip_network(fields[3], strict=False)
                except ValueError: raise ValueError('Hostnames or implicit networks require separate support.') from None
        found.append({'kind': kind, 'database': fields[1], 'user': fields[2],
                      'network': network, 'method': method})
    if not found: raise ValueError('Original host authentication is empty.')
    return found


def matches(rule, database, user, address, ssl):
    if rule['database'] not in {'all', database} or rule['user'] not in {'all', user}: return False
    if address is None: return rule['kind'] == 'local'
    if rule['kind'] == 'local': return False
    if rule['kind'] == 'hostssl' and not ssl: return False
    if rule['kind'] == 'hostnossl' and ssl: return False
    return rule['network'] is None or (address.version == rule['network'].version and address in rule['network'])


def render(original, database, admin, peers):
    """peers: exact IP/user/SSL triples, separately verified by the host caller.

    This renders no scope approval or IP-ownership evidence. Empty peers means
    local maintenance only. No global host-auth lines are replaced or loosened.
    """
    name(database); name(admin)
    if not isinstance(peers, list) or len(peers) > 16:
        raise ValueError('Bounded explicit application peer list required.')
    parsed = rules(original)
    local = next((item for item in parsed if matches(item, database, admin, None, False)), None)
    if not local or local['method'] == 'reject':
        raise ValueError('Existing local maintenance authentication must already permit this admin.')
    lines = ['# HexThings temporary exact-database maintenance barrier',
             f"local {database} {admin} {local['method']}"]
    seen = set()
    for peer in peers:
        if not isinstance(peer, dict) or set(peer) != {'address', 'user', 'ssl'} or type(peer['ssl']) is not bool:
            raise ValueError('Exact application address/user/SSL required.')
        user = name(peer['user'])
        if not isinstance(peer['address'], str) or '%' in peer['address']:
            raise ValueError('Literal unscoped application address required.')
        try: address = ipaddress.ip_address(peer['address'])
        except (ValueError, TypeError): raise ValueError('Literal application address required, not DNS/network.') from None
        if address.is_unspecified or address.is_multicast or address.is_loopback or (address.version == 6 and (address.is_link_local or address.ipv4_mapped)):
            raise ValueError('Unambiguous non-loopback application address required.')
        key = (str(address), user, peer['ssl'])
        if key in seen: raise ValueError('Duplicate application peer.')
        seen.add(key)
        matched = next((item for item in parsed if matches(item, database, user, address, peer['ssl'])), None)
        if not matched or matched['method'] != 'scram-sha-256':
            raise ValueError('Application peer must already match SCRAM authentication; no auth weakening.')
        kind = 'hostssl' if peer['ssl'] else 'hostnossl'
        lines.append(f'{kind} {database} {user} {address}/{address.max_prefixlen} scram-sha-256')
    lines += [f'local {database} all reject', f'host {database} all 0.0.0.0/0 reject',
              f'host {database} all ::/0 reject', '# Original host authentication follows unchanged']
    return ('\n'.join(lines) + '\n').encode() + original


class Barrier:
    """Private durable state precedes file replacement; no automatic restore.

    reload_and_verify(expected_bytes) must verify server syntax/reload and new
    positive+negative connections, not just pg_reload_conf() acknowledgement.
    It must raise on failure and return None. inventory() must rebind the exact
    host file to its currently running container, database and reviewed peers.
    A crash after rename/reload retains a staged record. Recovery accepts only
    the exact prior/candidate bytes, then installs and proves maintenance-only
    access before any new application handoff. External changes are not erased.
    """
    def __init__(self, journal, path, database, admin, inventory, reload_and_verify):
        self.journal = journal
        self.path = pathlib.Path(path).absolute()
        self.database, self.admin = name(database), name(admin)
        if not callable(inventory) or not callable(reload_and_verify):
            raise ValueError('Fresh host inventory and actual connection verifier required.')
        self.inventory, self.reload = inventory, reload_and_verify
        self.record_path = journal.directory / 'postgres-hba.json'

    def checked(self):
        self.journal.locked()
        # Do not make an unbound HBA write even with a reviewed-looking file.
        admission = state.read_private(self.journal.directory / 'maintenance.json')
        if admission.get('root') != str(self.journal.root):
            raise ValueError('Matching held deployment admission required.')
        if self.path.resolve() != self.path or self.path.is_symlink():
            raise ValueError('Host-auth file and parent path must not be symlinks.')
        info = self.path.stat()
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022 or info.st_nlink != 1:
            raise ValueError('Single-link protected regular host-auth file required.')
        if self.inventory() is not None:
            raise ValueError('Inventory verifier must raise, not return approval.')
        return self.path.read_bytes(), info

    def read(self):
        value = state.read_private(self.record_path)
        if (set(value) != {'version', 'path', 'database', 'admin', 'binding', 'original', 'current', 'pending', 'stage'}
                or value['version'] != 1 or value['path'] != str(self.path)
                or value['database'] != self.database or value['admin'] != self.admin
                or value['binding'] != state.read_private(self.journal.directory / 'maintenance.json')
                or value['stage'] not in {'staged', 'held', 'released'}):
            raise ValueError('Exact host-auth barrier record required.')
        for key in ['original', 'current', 'pending']:
            try: bytes.fromhex(value[key])
            except (ValueError, TypeError): raise ValueError('Corrupt host-auth barrier bytes.') from None
        return value

    def replace(self, value, desired):
        current, info = self.checked()
        permitted = [bytes.fromhex(value[key]) for key in ['current', 'pending']]
        if current not in permitted:
            raise ValueError('Host-auth changed outside this barrier; refuse to overwrite it.')
        value['pending'], value['stage'] = desired.hex(), 'staged'
        state.atomic(self.record_path, value)
        fd, temp = tempfile.mkstemp(prefix='.hexthings-hba-', dir=self.path.parent)
        try:
            with os.fdopen(fd, 'wb') as stream:
                os.fchown(stream.fileno(), info.st_uid, info.st_gid)
                os.fchmod(stream.fileno(), stat.S_IMODE(info.st_mode))
                stream.write(desired); stream.flush(); os.fsync(stream.fileno())
            # Revalidate immediately before replacement. Independently privileged
            # concurrent admin changes remain a host-boundary exclusion.
            live, _ = self.checked()
            if live != current: raise ValueError('Host-auth changed during file preparation.')
            os.replace(temp, self.path); state.sync_directory(self.path.parent)
        finally:
            if os.path.exists(temp): os.unlink(temp)
        if self.reload(desired) is not None:
            raise ValueError('Reload/connection verifier must raise, not return approval.')
        live, _ = self.checked()
        if live != desired: raise ValueError('Host-auth changed during reload verification.')
        value['current'], value['pending'], value['stage'] = desired.hex(), desired.hex(), 'held'
        state.atomic(self.record_path, value)

    def enter(self, recovery=False):
        original, _ = self.checked()
        if self.record_path.exists() or self.record_path.is_symlink():
            value = self.read()
            if not recovery or value['stage'] == 'released':
                raise ValueError('Existing barrier requires explicit unfinished recovery.')
            original = bytes.fromhex(value['original'])
        else:
            if recovery: raise ValueError('No host-auth barrier to recover.')
            render(original, self.database, self.admin, [])
            value = {'version': 1, 'path': str(self.path), 'database': self.database, 'admin': self.admin,
                     'binding': state.read_private(self.journal.directory / 'maintenance.json'),
                     'original': original.hex(), 'current': original.hex(), 'pending': original.hex(), 'stage': 'staged'}
            state.atomic(self.record_path, value)
        self.replace(value, render(original, self.database, self.admin, []))

    def handoff(self, peers):
        value = self.read()
        if value['stage'] != 'held': raise ValueError('Maintenance-only recovery must settle before handoff.')
        self.replace(value, render(bytes.fromhex(value['original']), self.database, self.admin, peers))

    def check(self):
        current, _ = self.checked(); value = self.read()
        if value['stage'] != 'held' or current != bytes.fromhex(value['current']):
            raise ValueError('Host-auth barrier is not held exactly.')
        if self.reload(current) is not None:
            raise ValueError('Live connection verifier must raise, not return approval.')

    def release(self):
        self.check(); value = self.read()
        self.replace(value, bytes.fromhex(value['original']))
        value = self.read(); value['stage'] = 'released'
        state.atomic(self.record_path, value)
        # Preserve evidence and the enclosing admission block until the caller
        # has accepted its runtime and durably settled all other protections.

    def recover_release(self):
        """Finish only a durably settled application release, never rebase HBA.

        Unlike enter(recovery=True), this does not close network admission or
        suggest schema retry. The exact original baseline and pending/current
        set remain bound to the same maintenance record through lost reload or
        released-marker acknowledgements.
        """
        self.journal.locked()
        transaction = self.journal.read()
        binding = state.read_private(self.journal.directory / 'maintenance.json')
        if (transaction.get('phase') not in {'committed', 'restored'}
                or transaction.get('kind') != 'application_bootstrap'
                or any(transaction.get(key) != binding.get(key) for key in
                       ['project', 'previous', 'candidate', 'receipt_sha256'])):
            raise ValueError('Exact durably settled application required for release recovery.')
        value = self.read()
        self.replace(value, bytes.fromhex(value['original']))
        value = self.read(); value['stage'] = 'released'
        state.atomic(self.record_path, value)
