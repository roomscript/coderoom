"""Opt-in real authentication probes; no credential output or state changes."""

import argparse
from contextlib import contextmanager
import json
import os
from pathlib import Path
import select
import shutil
import socket
import subprocess
import tempfile

from bubblewrap import command, environment
from experiment import authentication_result, fixture
from needs import Mount, Needs


REQUEST = ['/usr/bin/gh', 'api', '--hostname', 'github.com', '--method', 'GET', 'user', '--silent']
SERVICE = 'org.freedesktop.secrets'


def host_environment():
    env = dict(os.environ)
    # These probes specifically test stored credentials, not token forwarding.
    for name in ('GH_TOKEN', 'GITHUB_TOKEN'):
        env.pop(name, None)
    env['GH_NO_UPDATE_NOTIFIER'] = '1'
    env['GH_PROMPT_DISABLED'] = '1'
    return env


def auth_needs(home_read_only=False):
    home = Path.home().resolve()
    config = os.environ.get('GH_CONFIG_DIR') or str(
        Path(os.environ.get('XDG_CONFIG_HOME', str(home / '.config'))) / 'gh')
    needs = Needs(environment={'GH_CONFIG_DIR': config, 'GH_NO_UPDATE_NOTIFIER': '1',
                              'GH_PROMPT_DISABLED': '1'})
    if home_read_only:
        needs.mounts.append(Mount(home, home))
        needs.environment['HOME'] = str(home)
    path = Path(config)
    if path.is_dir():
        needs.mounts.append(Mount(path.resolve(), path))
    for name in ('/etc/resolv.conf', '/etc/hosts', '/etc/nsswitch.conf', '/etc/ssl/certs'):
        path = Path(name)
        if path.exists():
            needs.mounts.append(Mount(path.resolve(), path))
    return needs


def bus_address():
    address = os.environ.get('DBUS_SESSION_BUS_ADDRESS')
    if address:
        return address
    path = Path(f'/run/user/{os.getuid()}/bus')
    if not path.exists():
        raise RuntimeError('No host session bus is available')
    return f'unix:path={path}'


def bus_ping(project, needs, address, service, path, host=False):
    argv = ['/usr/bin/dbus-send', f'--bus={address}', '--print-reply',
            '--reply-timeout=3000', '--type=method_call', f'--dest={service}', path,
            'org.freedesktop.DBus.ListNames' if service == 'org.freedesktop.DBus'
            else 'org.freedesktop.DBus.Peer.Ping']
    if not host:
        argv = command(project, needs, argv)
    result = subprocess.run(argv, env=host_environment() if host else environment(needs),
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                            timeout=5, close_fds=True)
    return result.returncode == 0


def invalid_mutation_result(project, needs, address, host=False):
    # Missing mandatory arguments: reaching the real service cannot change an alias.
    argv = ['/usr/bin/dbus-send', f'--bus={address}', '--print-reply',
            '--reply-timeout=3000', '--type=method_call', f'--dest={SERVICE}',
            '/org/freedesktop/secrets', 'org.freedesktop.Secret.Service.SetAlias']
    if not host:
        argv = command(project, needs, argv)
    result = subprocess.run(argv, env=host_environment() if host else environment(needs),
                            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
                            text=True, timeout=5, close_fds=True)
    message = result.stderr.lower()
    if 'accessdenied' in message or 'not allowed' in message or 'access denied' in message:
        return 'proxy_denied'
    if 'invalidargs' in message or 'argument' in message or 'signature' in message or 'does not match expected type' in message:
        return 'service_rejected_missing_arguments'
    return 'unexpected_result'


def start_proxy(address, path, rules, parent, child):
    executable = shutil.which('xdg-dbus-proxy')
    if executable is None:
        raise RuntimeError('xdg-dbus-proxy is not installed')
    process = subprocess.Popen([executable, f'--fd={child.fileno()}', address, str(path),
                                '--filter', *rules], pass_fds=(child.fileno(),),
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                               start_new_session=True)
    child.close()
    if not select.select([parent], [], [], 5)[0] or not parent.recv(1):
        process.terminate()
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=3)
        raise RuntimeError('Filtered proxy did not signal readiness')
    return process


@contextmanager
def proxy_connection(root, project, rules, label):
    address = bus_address()
    directory = root / label
    directory.mkdir(mode=0o700)
    parent, child = socket.socketpair()
    process = None
    try:
        process = start_proxy(address, directory / 'bus', rules, parent, child)
        needs = auth_needs()
        needs.mounts.append(Mount(directory, Path('/run/coderoom-proxy')))
        needs.environment['DBUS_SESSION_BUS_ADDRESS'] = 'unix:path=/run/coderoom-proxy/bus'
        yield needs
    finally:
        parent.close()
        child.close()
        if process is not None:
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.terminate()
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)
            if process.poll() is None:
                raise RuntimeError('Proxy cleanup failed')


def retrieval_rules():
    # Permit retrieval, unlock, and session lifecycle, not secret creation/update/delete.
    methods = [('org.freedesktop.Secret.Service.OpenSession', '/org/freedesktop/secrets'),
               ('org.freedesktop.Secret.Service.SearchItems', '/org/freedesktop/secrets'),
               ('org.freedesktop.Secret.Service.GetSecrets', '/org/freedesktop/secrets'),
               ('org.freedesktop.Secret.Service.ReadAlias', '/org/freedesktop/secrets'),
               ('org.freedesktop.Secret.Service.Unlock', '/org/freedesktop/secrets'),
               ('org.freedesktop.Secret.Collection.SearchItems', '/org/freedesktop/secrets/*'),
               ('org.freedesktop.Secret.Item.GetSecret', '/org/freedesktop/secrets/*'),
               ('org.freedesktop.Secret.Session.Close', '/org/freedesktop/secrets/*'),
               ('org.freedesktop.DBus.Properties.Get', '/org/freedesktop/secrets/*'),
               ('org.freedesktop.DBus.Properties.GetAll', '/org/freedesktop/secrets/*'),
               ('org.freedesktop.DBus.Peer.Ping', '/org/freedesktop/secrets')]
    return [f'--call={SERVICE}={method}@{path}' for method, path in methods]


def filtered_auth(root, project):
    host_bus = bus_address()
    observations = {'case': 'keyring-proxy', 'token_forwarded': False,
                    'host': authentication_result(REQUEST, host_environment())}
    if observations['host'] != 'authenticated':
        print(json.dumps(observations))
        raise RuntimeError('Stored-credential host baseline failed; comparison is inconclusive')
    control_service, control_path = 'org.freedesktop.systemd1', '/org/freedesktop/systemd1'
    observations['unrelated_service_host_reachable'] = bus_ping(
        project, None, host_bus, control_service, control_path, host=True)
    observations['invalid_mutation_host'] = invalid_mutation_result(
        project, None, host_bus, host=True)
    for label, rules in [('service', [f'--talk={SERVICE}']), ('retrieval', retrieval_rules())]:
        with proxy_connection(root, project, rules, label) as needs:
            proxy_address = needs.environment['DBUS_SESSION_BUS_ADDRESS']
            observations[label + '_bus_reachable'] = bus_ping(
                project, needs, proxy_address, 'org.freedesktop.DBus', '/org/freedesktop/DBus')
            observations[label + '_authentication'] = authentication_result(
                command(project, needs, REQUEST), environment(needs))
            observations[label + '_unrelated_service_reachable'] = bus_ping(
                project, needs, proxy_address, control_service, control_path)
            observations[label + '_invalid_mutation'] = invalid_mutation_result(
                project, needs, proxy_address)
    print(json.dumps(observations))
    if observations['retrieval_authentication'] != 'authenticated':
        raise RuntimeError('Retrieval-only proxy did not restore authenticated gh access')
    if not observations['unrelated_service_host_reachable']:
        raise RuntimeError('Unrelated-service negative control lacks a host-positive baseline')
    if not observations['retrieval_bus_reachable']:
        raise RuntimeError('Negative controls lack a working sandbox D-Bus client baseline')
    if observations['invalid_mutation_host'] != 'service_rejected_missing_arguments':
        raise RuntimeError('Missing-argument mutation control did not reach the host service')
    if observations['retrieval_invalid_mutation'] != 'proxy_denied':
        raise RuntimeError('Mutation call header was not rejected by the narrow proxy')
    if observations['retrieval_unrelated_service_reachable']:
        raise RuntimeError('Unrelated host service was reachable through the filtered proxy')


def home_auth(project):
    needs = auth_needs(home_read_only=True)
    observations = {'case': 'home-read-only', 'token_forwarded': False,
                    'host': authentication_result(REQUEST, host_environment()),
                    'sandbox': authentication_result(command(project, needs, REQUEST), environment(needs))}
    print(json.dumps(observations))
    if observations['host'] != 'authenticated':
        raise RuntimeError('Stored-credential host baseline failed; comparison is inconclusive')
    # An authentication failure is an observation, not an expectation for every host.


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('case', choices=['home-read-only', 'keyring-proxy'])
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='cr-auth-', dir='/tmp') as directory:
        root = Path(directory)
        project, _, _ = fixture(root)
        if args.case == 'home-read-only':
            home_auth(project)
        else:
            filtered_auth(root, project)


if __name__ == '__main__':
    try:
        main()
    except (OSError, RuntimeError, subprocess.TimeoutExpired) as error:
        raise SystemExit(f'Experiment could not complete: {error}')
