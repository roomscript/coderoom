"""Observe filesystem, tool configuration, and Unix-socket behavior safely."""

import argparse
import array
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import uuid

from bubblewrap import command, environment, run
from needs import Mount, Needs
from tool_needs import gh_needs


def checked(result):
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or f'exit {result.returncode}')
    return result.stdout.strip()


def fixture(root):
    project = root / 'project'
    (project / '.coderoom').mkdir(parents=True)
    (project / '.coderoom' / 'policy').write_text('protected')
    config = root / 'config'
    config.mkdir()
    hidden = root / 'hidden'
    hidden.mkdir()
    (hidden / 'sentinel').write_text('synthetic host-only data')
    return project, config, hidden


def filesystem(project, config, hidden):
    runtime = project.parent / 'runtime'
    runtime.mkdir()
    sentinel = runtime / 'writable-on-host'
    sentinel.write_text('host-before')
    needs = Needs([Mount(config, config, writable=True), Mount(runtime, runtime)])
    code = '''
import errno, json
from pathlib import Path
project, config, hidden, runtime = map(Path, __import__('sys').argv[1:])
checks = {}
for name, path in [('project_write', project / 'output'),
                   ('config_write', config / 'state'),
                   ('private_home_write', Path.home() / 'scratch'),
                   ('private_tmp_write', Path('/tmp/scratch'))]:
    path.write_text('ok')
    checks[name] = path.read_text() == 'ok'
checks['host_hidden'] = not (hidden / 'sentinel').exists()
checks['runtime_readable'] = (runtime / 'writable-on-host').read_text() == 'host-before'
try:
    (project / '.coderoom/policy').write_text('changed')
    checks['control_write_blocked'] = False
except OSError:
    checks['control_write_blocked'] = True
try:
    (project / '.coderoom').rename(project / 'moved-control')
    checks['control_rename_blocked'] = False
except OSError:
    checks['control_rename_blocked'] = True
try:
    (runtime / 'writable-on-host').write_text('changed')
    checks['runtime_write_blocked'] = False
except OSError as error:
    checks['runtime_write_blocked'] = error.errno == errno.EROFS
print(json.dumps(checks))
'''
    checks = json.loads(checked(run(project, needs, ['/usr/bin/python3', '-c', code,
                                                    str(project), str(config), str(hidden), str(runtime)])))
    checks['runtime_unchanged'] = sentinel.read_text() == 'host-before'
    sentinel.write_text('host-after')
    checks['runtime_host_write'] = sentinel.read_text() == 'host-after'
    if not all(checks.values()):
        raise RuntimeError(f'filesystem checks failed: {checks}')
    print(json.dumps({'case': 'filesystem', **checks}))


def gh_configuration(project, config):
    binary = '/usr/bin/gh'
    if not Path(binary).is_file():
        raise RuntimeError('This fixture expects gh installed at /usr/bin/gh')
    (config / 'config.yml').write_text('editor: coderoom-synthetic-editor\n')
    mounted = Needs([Mount(config, config, writable=True)])
    get = [binary, 'config', 'get', 'editor']
    absent = run(project, mounted, get)
    environment_only = run(project, Needs(environment={'GH_CONFIG_DIR': str(config)}), get)
    complete = gh_needs(config)
    value = checked(run(project, complete, get))
    if value != 'coderoom-synthetic-editor':
        raise RuntimeError('gh did not read the mounted synthetic configuration')
    checked(run(project, complete, [binary, 'config', 'set', 'editor', 'coderoom-updated-editor']))
    # Confirm sandbox writes are visible to the normal CLI, without real auth/config.
    host_env = {'PATH': '/usr/bin:/bin', 'HOME': str(config.parent / 'host-home'),
                'GH_CONFIG_DIR': str(config), 'GH_NO_UPDATE_NOTIFIER': '1'}
    host = subprocess.run(get, env=host_env, text=True, capture_output=True, timeout=10)
    if checked(host) != 'coderoom-updated-editor':
        raise RuntimeError('configuration update was not visible outside the sandbox')
    replacement = config / 'replacement.yml'
    replacement.write_text('editor: coderoom-refreshed-editor\n')
    replacement.replace(config / 'config.yml')
    if checked(run(project, complete, get)) != 'coderoom-refreshed-editor':
        raise RuntimeError('atomic configuration replacement was not visible')
    if absent.stdout.strip() == value or environment_only.stdout.strip() == value:
        raise RuntimeError('negative configuration controls unexpectedly read the fixture')
    print(json.dumps({'case': 'gh', 'mount_only_finds_config': False,
                      'env_only_finds_config': False, 'mount_and_env_finds_config': True,
                      'outside_continuation': True, 'atomic_replacement': True,
                      'real_authentication_tested': False}))


def authentication_result(argv, env):
    result = subprocess.run(argv, env=env, stdout=subprocess.DEVNULL,
                            stderr=subprocess.PIPE, text=True, timeout=20, close_fds=True)
    if result.returncode == 0:
        return 'authenticated'
    message = result.stderr.lower()
    if any(text in message for text in ('no such host', 'lookup ', 'network is unreachable',
                                       'connection refused', 'connect:', 'timeout')):
        return 'network_failure'
    if any(text in message for text in ('certificate', 'x509', 'tls')):
        return 'tls_failure'
    if any(text in message for text in ('gh auth login', 'authentication token',
                                       'http 401', 'http 403', 'not logged')):
        return 'authentication_failure'
    return 'request_failed'


def gh_authentication(project, export_host_token=False):
    binary = '/usr/bin/gh'
    selected = os.environ.get('GH_CONFIG_DIR')
    if not selected:
        selected = str(Path(os.environ.get('XDG_CONFIG_HOME', str(Path.home() / '.config'))) / 'gh')
    config = Path(selected).resolve()
    needs = Needs(environment={'GH_CONFIG_DIR': str(config),
                              'GH_NO_UPDATE_NOTIFIER': '1', 'GH_PROMPT_DISABLED': '1'})
    if config.is_dir():
        # Read-only: this probe does not refresh or change real authentication state.
        needs.mounts.append(Mount(config, config))
    tokens = {name: os.environ[name] for name in ('GH_TOKEN', 'GITHUB_TOKEN') if os.environ.get(name)}
    credential_source = 'environment' if tokens else 'configuration_only'
    if export_host_token and not tokens:
        token = subprocess.run([binary, 'auth', 'token', '--hostname', 'github.com'],
                               stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                               text=True, timeout=10, close_fds=True)
        if token.returncode or not token.stdout.strip():
            raise RuntimeError('Could not retrieve host credential for explicit export probe')
        tokens['GH_TOKEN'] = token.stdout.strip()
        credential_source = 'host_token_export'
    needs.environment.update(tokens)
    # Required infrastructure for real HTTPS requests, independent of gh credentials.
    for name in ('/etc/resolv.conf', '/etc/hosts', '/etc/nsswitch.conf', '/etc/ssl/certs'):
        path = Path(name)
        if path.exists():
            needs.mounts.append(Mount(path.resolve(), path))
    request = [binary, 'api', '--hostname', 'github.com', '--method', 'GET', 'user', '--silent']
    host_env = dict(os.environ)
    host_env.update(needs.environment)
    observations = {'case': 'gh-auth', 'config_mounted_read_only': config.is_dir(),
                    'credential_source': credential_source,
                    'host': authentication_result(request, host_env)}
    if observations['host'] != 'authenticated':
        print(json.dumps(observations))
        raise RuntimeError('Host authentication baseline failed; sandbox comparison is inconclusive')
    config_only = Needs(needs.mounts, {key: value for key, value in needs.environment.items()
                                     if key not in ('GH_TOKEN', 'GITHUB_TOKEN')})
    observations['sandbox_config_only'] = authentication_result(
        command(project, config_only, request), environment(config_only))
    observations['sandbox_config_and_environment'] = authentication_result(
        command(project, needs, request), environment(needs))
    print(json.dumps(observations))
    if observations['sandbox_config_and_environment'] != 'authenticated':
        raise RuntimeError('Sandbox authenticated request failed; inspect the reported category')


def socket_client_code():
    return '''
import array, json, socket, sys, time
from pathlib import Path
address, ready, late = sys.argv[1:]
if ready:
    Path(ready).write_text('ready')
if address.startswith('@'):
    address = '\\0' + address[1:]
connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
connection.settimeout(2)
deadline = time.monotonic() + (5 if late == 'yes' else 0)
while True:
    try:
        connection.connect(address)
        break
    except OSError:
        if time.monotonic() >= deadline:
            print(json.dumps({'connected': False, 'host_descriptor_read': False}))
            sys.exit(0)
        time.sleep(0.02)
_, ancillary, _, _ = connection.recvmsg(1, socket.CMSG_SPACE(array.array('i').itemsize))
read = False
for level, kind, data in ancillary:
    if level == socket.SOL_SOCKET and kind == socket.SCM_RIGHTS:
        descriptors = array.array('i')
        descriptors.frombytes(data[:len(data) - len(data) % descriptors.itemsize])
        for descriptor in descriptors:
            with __import__('os').fdopen(descriptor) as source:
                read = source.read() == 'synthetic host-only data'
print(json.dumps({'connected': True, 'host_descriptor_read': read}))
'''


def serve_descriptor(listener, hidden, errors):
    try:
        listener.settimeout(8)
        try:
            connection, _ = listener.accept()
        except TimeoutError:
            return
        with connection, (hidden / 'sentinel').open() as source:
            descriptors = array.array('i', [source.fileno()])
            connection.sendmsg([b'x'], [(socket.SOL_SOCKET, socket.SCM_RIGHTS, descriptors)])
    except Exception as error:
        errors.append(error)


def socket_probe(project, config, hidden, label, address, late=False, private_network=False):
    needs = Needs([Mount(config, config, writable=True)])
    ready = project / f'{label}.ready'
    listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    bind_address = '\0' + address[1:] if address.startswith('@') else address
    process, thread, errors = None, None, []
    try:
        if not late:
            listener.bind(bind_address)
            listener.listen(1)
        args = command(project, needs, ['/usr/bin/python3', '-c', socket_client_code(),
                                       address, str(ready) if late else '',
                                       'yes' if late else 'no'], private_network)
        process = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, close_fds=True, env=environment(needs))
        if late:
            deadline = time.monotonic() + 5
            while not ready.exists():
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError('sandbox did not reach the late-socket checkpoint')
                time.sleep(0.02)
            listener.bind(bind_address)
            listener.listen(1)
        thread = threading.Thread(target=serve_descriptor, args=(listener, hidden, errors))
        thread.start()
        stdout, stderr = process.communicate(timeout=10)
        if process.returncode:
            raise RuntimeError(stderr.strip())
        observation = json.loads(stdout)
        if observation['connected']:
            thread.join(timeout=2)
        print(json.dumps({'case': 'sockets', 'channel': label,
                          'network': 'private' if private_network else 'host', **observation}))
    finally:
        if process is not None and process.poll() is None:
            process.kill()
            process.communicate()
        if thread is not None:
            thread.join(timeout=9)
        listener.close()
        if errors:
            raise RuntimeError(f'synthetic descriptor server failed: {errors[0]}')


def sockets(project, config, hidden):
    for label, address, late, private in [
        ('project-path', str(project / 'service.sock'), False, False),
        ('config-path', str(config / 'service.sock'), False, False),
        ('late-project-path', str(project / 'late.sock'), True, False),
        ('abstract-host', '@coderoom-' + uuid.uuid4().hex, False, False),
        ('abstract-private', '@coderoom-' + uuid.uuid4().hex, False, True),
        ('path-private', str(project / 'private-net.sock'), False, True),
    ]:
        socket_probe(project, config, hidden, label, address, late, private)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('case', choices=['filesystem', 'gh', 'gh-auth', 'sockets', 'all'], default='all', nargs='?')
    parser.add_argument('--export-host-token', action='store_true',
                        help='gh-auth only: retrieve host credential and pass it via child environment')
    args = parser.parse_args()
    if args.export_host_token and args.case != 'gh-auth':
        parser.error('--export-host-token requires gh-auth')
    # Short paths keep pathname socket names below Linux's address limit.
    with tempfile.TemporaryDirectory(prefix='cr-bwrap-', dir='/tmp') as directory:
        project, config, hidden = fixture(Path(directory))
        if args.case == 'gh-auth':
            gh_authentication(project, args.export_host_token)
            return
        cases = {'filesystem': lambda: filesystem(project, config, hidden),
                 'gh': lambda: gh_configuration(project, config),
                 'sockets': lambda: sockets(project, config, hidden)}
        for name, execute in cases.items():
            if args.case in ('all', name):
                execute()


if __name__ == '__main__':
    try:
        main()
    except (OSError, RuntimeError, subprocess.TimeoutExpired) as error:
        raise SystemExit(f'Experiment could not complete: {error}')
