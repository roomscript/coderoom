"""Disposable provider experiment; not a production sandbox or agent API."""

from pathlib import Path
import shutil
import subprocess

from needs import Needs



def command(project: Path, needs: Needs, argv: list[str], private_network=False):
    """Only this module knows the Bubblewrap technology and launch flags."""
    executable = shutil.which('bwrap')
    if executable is None:
        raise RuntimeError('Bubblewrap is not installed')
    args = [executable, '--unshare-user', '--unshare-pid', '--unshare-ipc',
            '--cap-drop', 'ALL', '--die-with-parent', '--new-session']
    if private_network:
        args += ['--unshare-net']
    for name in ('usr', 'bin', 'sbin', 'lib', 'lib64'):
        source = Path('/') / name
        if source.exists():
            args += ['--ro-bind', str(source), str(source)]
    args += ['--proc', '/proc', '--dev', '/dev', '--tmpfs', '/tmp',
             '--dir', '/home/probe', '--bind', str(project), str(project),
             '--ro-bind', str(project / '.coderoom'), str(project / '.coderoom')]
    for mount in needs.mounts:
        args += ['--bind' if mount.writable else '--ro-bind',
                 str(mount.source), str(mount.destination)]
    return args + ['--chdir', str(project), '--', *argv]


def environment(needs):
    # Construct a clean environment at the launch boundary. Secrets stay out of argv.
    return {'HOME': '/home/probe', 'TMPDIR': '/tmp', 'PATH': '/usr/bin:/bin',
            'LANG': 'C.UTF-8', **needs.environment}


def run(project, needs, argv, private_network=False):
    return subprocess.run(command(project, needs, argv, private_network),
                          text=True, capture_output=True, timeout=15, close_fds=True, env=environment(needs))
