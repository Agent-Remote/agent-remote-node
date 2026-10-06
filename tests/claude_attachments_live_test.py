#!/usr/bin/env python3
"""Opt-in real Claude Read test; run as root on a Linux Node with bwrap.

AR_ATTACHMENT_TEST_ACCOUNT: existing authenticated account (never modified)
AR_ATTACHMENT_TEST_USER: non-root runtime username
AR_ATTACHMENT_TEST_CLAUDE: managed Claude executable
Uses temporary credential copies, no session persistence, and Read-only tool use.
Both Native /account and Sandbox absolute account path layouts are exercised in
Bubblewrap; this does not assert Docker Sandbox availability on the host.
"""
import json
import os
from pathlib import Path
import pwd
import secrets
import shutil
import struct
import subprocess
import tempfile
import zlib


def png():
    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack(
            '>I', zlib.crc32(kind + data) & 0xffffffff)
    return (b'\x89PNG\r\n\x1a\n' +
            chunk(b'IHDR', struct.pack('>IIBBBBB', 8, 8, 8, 2, 0, 0, 0)) +
            chunk(b'IDAT', zlib.compress((b'\0' + b'\xff\0\0' * 8) * 8)) +
            chunk(b'IEND', b''))


def check_layout(account, user, claude, layout):
    identity = pwd.getpwnam(user)
    if identity.pw_uid == 0:
        raise RuntimeError('test runtime must be non-root')
    with tempfile.TemporaryDirectory(prefix='ar-attachment-read-', dir='/var/tmp') as temporary:
        root = Path(temporary)
        config = root / 'home/.claude'
        workspace = root / 'workspace'
        attachments = root / 'account/.agent-remote-attachments'
        for directory in (config, workspace, attachments / 'session', attachments / 'other'):
            directory.mkdir(parents=True, exist_ok=True)
        for source, name in ((account / '.claude/.credentials.json', '.credentials.json'),
                             (account / '.claude.json', '.claude.json')):
            if source.is_file():
                shutil.copyfile(source, config / name)
        (config / 'settings.json').write_text(json.dumps({
            'model': 'haiku', 'permissions': {'blockReadsOutsideWorkingDirectories': True}}))
        markers = {name: secrets.token_hex(16) for name in ('session', 'other')}
        for name, marker in markers.items():
            (attachments / name / '示例 file.js').write_text(f'export const marker = "{marker}";\n')
        (attachments / 'session/image.png').write_bytes(png())
        for path in (root, *root.rglob('*')):
            os.chown(path, identity.pw_uid, identity.pw_gid)
            path.chmod(0o700 if path.is_dir() else 0o600)
        visible = '/account' if layout == 'native' else str(root / 'account')
        command = ['runuser', '-u', user, '--', 'bwrap', '--die-with-parent', '--unshare-pid', '--unshare-user']
        for directory in ('/usr', '/bin', '/lib', '/lib64', '/etc', '/opt'):
            if Path(directory).exists():
                command += ['--ro-bind', directory, directory]
        command += ['--tmpfs', '/tmp', '--bind', str(root / 'home'), '/test-home',
                    '--bind', str(root / 'account'), visible, '--bind', str(workspace), '/workspace',
                    '--proc', '/proc', '--dev', '/dev', '--chdir', '/workspace',
                    '--setenv', 'HOME', '/test-home', '--setenv', 'CLAUDE_CONFIG_DIR', '/test-home/.claude',
                    '--', claude, '--print', '--no-session-persistence', '--tools', 'Read',
                    '--allowedTools', 'Read', '--permission-mode', 'dontAsk', '--strict-mcp-config',
                    '--mcp-config', '{"mcpServers":{}}', '--output-format', 'stream-json', '--verbose', '--max-turns', '4']
        for case, grant, target in (('control', False, 'session'), ('granted', True, 'session'),
                                    ('other-session', True, 'other'), ('image', True, 'session')):
            filename = 'image.png' if case == 'image' else '示例 file.js'
            path = f'{visible}/.agent-remote-attachments/{target}/{filename}'
            prompt = f'Use the Read tool to read exactly {json.dumps(path, ensure_ascii=False)}. '
            prompt += 'Describe the image color.' if case == 'image' else 'Report its exact marker. Do not guess.'
            args = command + [prompt]
            if grant:
                args += [f'--add-dir={visible}/.agent-remote-attachments/session']
            result = subprocess.run(args, capture_output=True, text=True, timeout=120)
            events = []
            for line in result.stdout.splitlines():
                try:
                    events.append(json.loads(line))
                except ValueError:
                    pass
            reads = [part for event in events if event.get('type') == 'user'
                     for part in event.get('message', {}).get('content', []) if part.get('type') == 'tool_result']
            if case == 'image':
                success = any(isinstance(read.get('content'), list) and
                              any(part.get('type') == 'image' for part in read['content'])
                              and not read.get('is_error') for read in reads)
            elif case == 'granted':
                success = any(markers[target] in json.dumps(read) and not read.get('is_error') for read in reads)
            else:
                success = (markers[target] not in result.stdout and
                           any(read.get('is_error') and 'blockReadsOutsideWorkingDirectories' in json.dumps(read)
                               for read in reads))
            # Do not print Claude output, copied configuration, or credentials.
            print(f'{layout}/{case}: exit={result.returncode}, read_results={len(reads)}, passed={success}', flush=True)
            if result.returncode or not success:
                raise RuntimeError(f'{layout}/{case} failed the actual Claude Read contract')


if __name__ == '__main__':
    for runtime_layout in ('native', 'sandbox'):
        check_layout(Path(os.environ['AR_ATTACHMENT_TEST_ACCOUNT']),
                     os.environ['AR_ATTACHMENT_TEST_USER'], os.environ['AR_ATTACHMENT_TEST_CLAUDE'], runtime_layout)
    print('All actual Claude Read contracts passed; temporary state removed.')
