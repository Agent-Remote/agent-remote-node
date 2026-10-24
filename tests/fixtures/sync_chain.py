"""Exercise real synchronization components with disposable credentials and state.

The controller currently requires macOS Docker Desktop: its host gateway reaches
the loopback-only FastAPI listener. Linux is the actual Node runtime under test.
No mock control-plane responses, SSH shim, privileged host changes, or real
account credentials are used. The shell launcher removes every temporary file.
"""

from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import socket
import subprocess
import sys
import tarfile
import threading
import time
from uuid import UUID, uuid4


def run(args, *, env=None, timeout=90, data=None, check=True):
    """Capture subprocess output so credentials never enter the test transcript."""
    result = subprocess.run(
        args, input=data, text=True, capture_output=True, env=env, timeout=timeout
    )
    if check and result.returncode:
        # Command names and bounded public diagnostics suffice to locate failure.
        raise RuntimeError(
            f"{Path(args[0]).name} exited {result.returncode}: {result.stderr[-1200:]}"
        )
    return result


def wait_for(predicate, label, seconds=40):
    """Use bounded polling; never treat time elapsed as successful verification."""
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.25)
    raise AssertionError(f"timed out: {label}")


def main():
    """Build isolated identities, start real components, and prove sync invariants."""
    parser = argparse.ArgumentParser(description=__doc__)
    for argument in ("root", "node-repo", "server-repo", "cli", "architecture"):
        parser.add_argument(f"--{argument}", required=True)
    options = parser.parse_args()
    if platform.system() != "Darwin":
        raise SystemExit(
            "Controller requires macOS Docker Desktop; Node executes on real Linux."
        )
    root = Path(options.root).resolve()
    source_cli = Path(options.cli).resolve()
    client_bin = root / "client-bin"
    client_bin.mkdir()
    for name in ("agent-remote", "agent-remote-ssh", "agent-remote-scp"):
        shutil.copy2(source_cli.parent / name, client_bin / name)
    cli = client_bin / "agent-remote"
    print(
        "CLI fixture SHA-256:", hashlib.sha256(cli.read_bytes()).hexdigest(), flush=True
    )
    sys.path.insert(0, str(Path(options.server_repo) / "src"))
    import httpx
    import uvicorn
    from sqlalchemy import select
    from agent_remote_server.config import Settings
    from agent_remote_server.db import Base
    from agent_remote_server.main import create_app
    from agent_remote_server.models import SshKey

    app = create_app(
        Settings(
            _env_file=None,
            secret_key=uuid4().hex,
            log_level="CRITICAL",
            database_url=f"sqlite+aiosqlite:///{root}/server.db",
            redis_url="redis://127.0.0.1:1/0",
            node_offline_after_seconds=600,
        )
    )

    async def schema():
        """Create only the ephemeral database, using current production models."""
        async with app.state.database_engine.begin() as connection:
            await connection.run_sync(Base.metadata.create_all)

    asyncio.run(schema())
    listener = socket.socket()
    listener.bind(("127.0.0.1", 0))
    port = listener.getsockname()[1]
    server = uvicorn.Server(uvicorn.Config(app, log_level="critical", access_log=False))
    server_thread = threading.Thread(
        target=server.run, kwargs={"sockets": [listener]}, daemon=True
    )
    server_thread.start()
    wait_for(lambda: server.started, "temporary Server startup")
    base = f"http://127.0.0.1:{port}"
    container = "agent-remote-sync-proof-" + uuid4().hex[:12]
    (root / "container-name").write_text(container)
    stop_poll = threading.Event()
    poll_thread = None
    worker_generation = 0
    environment = os.environ.copy()
    environment.update(
        AGENT_REMOTE_HOME=str(root / "home"),
        AGENT_REMOTE_SECRET_BACKEND="file",
        MUTAGEN_DATA_DIRECTORY=str(root / "mutagen"),
    )
    environment.pop("SSH_AUTH_SOCK", None)
    environment.pop("SSH_AGENT_PID", None)
    client = httpx.Client(base_url=base, timeout=20)

    def api(method, path, body=None, token=None):
        """Use the actual HTTP API without exposing synthetic credential bodies."""
        headers = {"Authorization": "Bearer " + token} if token else {}
        response = client.request(method, path, json=body, headers=headers)
        if response.status_code != 200:
            raise AssertionError(f"HTTP {method} {path}: {response.status_code}")
        return response.json()["data"]

    def docker(*args, **kwargs):
        """Run a command only in this uniquely named disposable container."""
        return run(["docker", "exec", "--interactive", container, *args], **kwargs)

    def control(method, params):
        """Exercise the production desktop-control protocol through the real CLI."""
        request = {
            "protocol_version": 1,
            "request_id": uuid4().hex,
            "method": method,
            "params": params,
        }
        result = run(
            [str(cli), "--desktop-control"],
            env=environment,
            data=json.dumps(request),
            timeout=145,
            check=False,
        )
        response = json.loads(result.stdout)
        if result.returncode or response["type"] != "result":
            raise AssertionError(f"{method}: {response.get('error')}")
        return response["result"]

    try:
        print(
            "Starting disposable Linux sshd, worker and privileged helper", flush=True
        )
        run(
            [
                "docker",
                "run",
                "--detach",
                "--rm",
                "--name",
                container,
                "--cap-add",
                "SYS_ADMIN",
                "--security-opt",
                "seccomp=unconfined",
                "--security-opt",
                "apparmor=unconfined",
                "--security-opt",
                "systempaths=unconfined",
                "--publish",
                "127.0.0.1::22",
                "--mount",
                f"type=bind,src={root}/bin,dst=/proof,readonly",
                "--mount",
                f"type=bind,src={options.node_repo}/tests/fixtures/sync_chain_container.sh,dst=/init-proof,readonly",
                "debian:bookworm-slim",
                "/bin/sh",
                "/init-proof",
            ],
            timeout=120,
        )
        wait_for(
            lambda: (
                docker("test", "-f", "/tmp/sync-proof-ready", check=False).returncode
                == 0
            ),
            "container packages and identities",
            240,
        )
        ssh_port = int(
            run(["docker", "port", container, "22/tcp"])
            .stdout.strip()
            .rsplit(":", 1)[1]
        )
        admin = api(
            "POST",
            "/api/v1/auth/bootstrap",
            {"username": "proof-admin", "password": uuid4().hex},
        )
        admin_token = admin["access_token"]
        # Password remains memory-only and goes to CLI over stdin.
        password = uuid4().hex
        # Use the real user endpoint for the local-device identity under test.
        user = api(
            "POST",
            "/api/v1/users",
            {"username": "proof-user", "password": password, "role": "user"},
            admin_token,
        )
        user_id = user["id"]
        node = api(
            "POST",
            "/api/v1/nodes",
            {
                "name": "sync-proof",
                "region_code": "US",
                "weight": 100,
                "supported_tool_types": ["claude"],
                "wireguard_ip": "10.42.0.10",
                "wireguard_public_key": "synthetic-node-wg",
                "wireguard_endpoint": "127.0.0.1:51820",
                "ssh_host": "127.0.0.1",
                "ssh_port": ssh_port,
                "ssh_user": "agent-remote",
            },
            admin_token,
        )
        node_id = node["node"]["id"]
        registered = api(
            "POST",
            "/api/v1/node-api/register",
            {
                "node_id": node_id,
                "registration_token": node["registration_token"],
                "version": "sync-proof",
            },
        )
        config = {
            "server_url": f"http://host.docker.internal:{port}",
            "node_id": node_id,
            "node_token": registered["node_token"],
            "ledger_path": "/var/lib/agent-remote-node/ledger.json",
            "ssh_authorized_keys_path": "/var/lib/agent-remote-node/authorized_keys",
            "attach_binary_path": "/usr/local/bin/agent-remote-attach",
            "runtime_socket_path": "/run/agent-remote/runtime.sock",
            "runtime_binary_path": "/usr/local/bin/agent-remote-runtime",
            "allowed_runtime_backends": ["native"],
            "skill_manager_enabled": False,
        }
        config_path = root / "node-config.json"
        config_path.write_text(json.dumps(config))
        config_path.chmod(0o600)
        run(
            [
                "docker",
                "cp",
                str(config_path),
                container + ":/etc/agent-remote-node/config.json",
            ]
        )
        docker("chown", "root:agent-remote", "/etc/agent-remote-node/config.json")
        docker("chmod", "0640", "/etc/agent-remote-node/config.json")
        run(["docker", "exec", "--detach", container, "agent-remote-runtime", "serve"])
        wait_for(
            lambda: (
                docker(
                    "test", "-S", "/run/agent-remote/runtime.sock", check=False
                ).returncode
                == 0
            ),
            "root-owned helper socket",
        )
        docker("/usr/sbin/sshd", "-E", "/tmp/sshd.log")
        poll_errors = []

        def poll():
            """Execute real leased Node work as the unprivileged service identity."""
            while not stop_poll.is_set():
                result = docker(
                    "runuser",
                    "-u",
                    "agent-remote",
                    "--",
                    "agent-remote-node",
                    "poll-once",
                    "--config",
                    "/etc/agent-remote-node/config.json",
                    check=False,
                )
                if result.returncode:
                    poll_errors.append(result.stderr[-1000:])
                stop_poll.wait(0.3)

        def start_worker():
            """Start a fresh real Node worker poll lifecycle."""
            nonlocal poll_thread, worker_generation
            if poll_thread is not None and poll_thread.is_alive():
                raise AssertionError("Node worker is already running")
            stop_poll.clear()
            worker_generation += 1
            poll_thread = threading.Thread(
                target=poll,
                name=f"sync-proof-node-worker-{worker_generation}",
                daemon=True,
            )
            poll_thread.start()

        def stop_worker():
            """Stop the current worker and wait for its process loop to exit."""
            nonlocal poll_thread
            stop_poll.set()
            if poll_thread is not None:
                poll_thread.join(95)
                if poll_thread.is_alive():
                    raise AssertionError("Node worker did not stop within its bounded timeout")
                poll_thread = None

        start_worker()
        key = root / "identity"
        run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)])
        for name in ("ssh", "scp"):
            wrapper = root / ("system-" + name)
            wrapper.write_text(
                "#!/bin/sh\nexec /usr/bin/"
                + name
                + " -F /dev/null -i "
                + shlex.quote(str(key))
                + ' -o IdentitiesOnly=yes -o BatchMode=yes "$@"\n'
            )
            wrapper.chmod(0o700)
            environment["AGENT_REMOTE_SYSTEM_" + name.upper()] = str(wrapper)
        binary_dir = root / "home" / "bin"
        binary_dir.mkdir(parents=True)
        shutil.copy2(root / "bin" / "mutagen", binary_dir / "mutagen")
        # Match the production CLI's Mutagen namespace: the daemon must use
        # the fixture's fixed SSH/SCP siblings after it is restarted too.
        environment["MUTAGEN_SSH_PATH"] = str(binary_dir)
        environment["PATH"] = str(binary_dir) + os.pathsep + environment.get("PATH", "")
        for name in ("ssh", "scp"):
            shutil.copy2(cli.parent / ("agent-remote-" + name), binary_dir / name)
        with tarfile.open(binary_dir / "mutagen-agents.tar.gz", "w:gz") as archive:
            archive.add(
                root / "bin" / ("linux_" + options.architecture),
                arcname="linux_" + options.architecture,
            )
        control(
            "login.password",
            {
                "server_url": base,
                "username": "proof-user",
                "password": password,
                "ssh_public_key": str(key) + ".pub",
                "skip_device_registration": False,
                "confirmed": True,
                "expected_context": {"server_url": None, "active_device_id": None},
            },
        )
        context = control("context", {})
        project = root / "project"
        project.mkdir()
        (project / "from-local.txt").write_text("local baseline\n")
        query = {"expected_context": context, "local_path": str(project)}

        def action(name):
            """Refresh and confirm the exact current target before each mutation."""
            current = control("sync.status", query)
            receipt = control(
                "sync." + name,
                {
                    "expected_context": context,
                    "expected_target": current["target"],
                    "confirmed": True,
                    "dry_run": False,
                },
            )
            if receipt["outcome"] != "completed":
                raise AssertionError(
                    f"sync.{name}: {receipt.get('error_code')}; stages={receipt.get('stages')}; worker={poll_errors[-1:]}"
                )
            return receipt

        receipt = action("ensure")
        remote_path = receipt["sync"]["remote_path"]
        runtime_user = "ar-u-" + hashlib.sha256(user_id.encode()).hexdigest()[:12]

        def remote_read(name):
            """Observe the prepared real filesystem without modifying its permissions."""
            value = docker("cat", remote_path + "/" + name, check=False)
            return value.stdout if value.returncode == 0 else None

        def remote_write(name, value):
            """Model a remote Agent file edit under its actual non-root runtime UID."""
            docker(
                "runuser",
                "-u",
                runtime_user,
                "--",
                "python3",
                "-c",
                "import pathlib,sys;pathlib.Path(sys.argv[1]).write_text(sys.stdin.read())",
                remote_path + "/" + name,
                data=value,
            )

        wait_for(
            lambda: remote_read("from-local.txt") == "local baseline\n",
            "local to Node transfer",
        )
        remote_write("from-node.txt", "remote baseline\n")
        wait_for(lambda: (project / "from-node.txt").exists(), "Node to local transfer")
        assert (project / "from-node.txt").read_text() == "remote baseline\n"
        # Preparation is proven by its task receipt and runtime ownership, not mkdir in the fixture.
        uid = docker("id", "-u", runtime_user).stdout.strip()
        assert uid != "0"
        assert docker("stat", "-c", "%u", remote_path).stdout.strip() == uid
        assert "user:" in docker("getfacl", "-cp", remote_path).stdout
        marker = json.loads(remote_read(".agent-remote-workspace.json"))
        assert marker["user_id"] == user_id
        assert marker["workspace_id"] == receipt["sync"]["workspace_id"]
        assert marker["sync_session_id"] == receipt["sync"]["id"]
        print(
            "PASS real preparation, runtime UID/ACL and bidirectional SSH/Mutagen transfer",
            flush=True,
        )

        # Restart proof deliberately covers only a bounded, observable window:
        # stop the real Node worker poll loop, stop the isolated Mutagen daemon
        # with autostart disabled, restart Mutagen and verify its persisted
        # session inventory, then start a fresh worker loop and complete a new
        # real pause/resume transfer.  This does not claim recovery from an
        # arbitrary crash in the middle of an unknown mutation; that stronger
        # guarantee needs a separately controlled lease/interruption fixture.
        mutagen = root / "home/bin/mutagen"
        mutagen_session_id = receipt["sync"].get("mutagen_session_id")
        if not isinstance(mutagen_session_id, str) or not mutagen_session_id:
            raise AssertionError("sync receipt omitted Mutagen session identity")

        def mutagen_run(*args, disabled_autostart=False, timeout=30):
            env = dict(environment)
            if disabled_autostart:
                env["MUTAGEN_DISABLE_AUTOSTART"] = "1"
            return run([str(mutagen), *args], env=env, timeout=timeout, check=False)

        stop_worker()
        stopped = mutagen_run("daemon", "stop", timeout=20)
        if stopped.returncode != 0:
            raise AssertionError(
                "Mutagen daemon stop failed; restart recovery was not claimed"
            )
        unavailable = mutagen_run(
            "sync", "list", "--long", disabled_autostart=True, timeout=20
        )
        if unavailable.returncode == 0:
            raise AssertionError(
                "Mutagen sync list succeeded while daemon was stopped with autostart disabled"
            )
        started = mutagen_run("daemon", "start", timeout=30)
        if started.returncode != 0:
            raise AssertionError("Mutagen daemon restart failed")

        def restarted_session_is_visible():
            observed = mutagen_run("sync", "list", "--long", timeout=20)
            return observed.returncode == 0 and mutagen_session_id in observed.stdout

        wait_for(
            restarted_session_is_visible,
            "Mutagen persisted session inventory after daemon restart",
            45,
        )
        # An external daemon restart creates a new generation. Recover the
        # reviewed namespace explicitly before project mutations; sync actions
        # must keep rejecting an unreviewed generation mismatch.
        environment_plan = control(
            "mutagen.environment.preview",
            {"expected_home": str(root / "home")},
        )
        environment_receipt = control(
            "mutagen.environment.apply",
            {
                "expected_home": str(root / "home"),
                "expected_fingerprint": environment_plan["fingerprint"],
                "confirmed": True,
            },
        )
        if environment_receipt.get("outcome") != "completed":
            raise AssertionError(
                "Mutagen environment recovery failed: "
                + str(environment_receipt.get("error_code"))
            )
        start_worker()
        if worker_generation < 2 or poll_thread is None:
            raise AssertionError("fresh Node worker generation did not start")
        restart_marker = "after-worker-mutagen-restart.txt"
        action("pause")
        (project / restart_marker).write_text("restart recovery\n")
        time.sleep(1)
        assert remote_read(restart_marker) is None
        action("resume")
        wait_for(
            lambda: remote_read(restart_marker) == "restart recovery\n",
            "post-restart pause/resume transfer",
        )
        # Keep the status line machine-exact; details are a separate bounded,
        # non-authoritative diagnostic so wrapper parsing cannot match a user
        # controlled payload or a substring of another log line.
        print("RESTART_RECOVERY_STATUS=passed", flush=True)
        print(
            "RESTART_RECOVERY_DETAILS=worker_generation=%d mutagen_session_persisted=1"
            % worker_generation,
            flush=True,
        )
        action("pause")
        paused = control("sync.status", query)
        assert (
            paused["sync"]["status"] == "paused" and paused["mutagen"]["paused"] is True
        )
        (project / "while-paused.txt").write_text("queued\n")
        time.sleep(1)
        assert remote_read("while-paused.txt") is None
        action("resume")
        wait_for(
            lambda: remote_read("while-paused.txt") == "queued\n", "resume transfer"
        )
        action("pause")
        (project / "from-local.txt").write_text("local conflicting edit\n")
        remote_write("from-local.txt", "remote conflicting edit\n")
        # Resume may report a truthful conflict receipt; neither side may be selected automatically.
        current = control("sync.status", query)
        control(
            "sync.resume",
            {
                "expected_context": context,
                "expected_target": current["target"],
                "confirmed": True,
                "dry_run": False,
            },
        )
        wait_for(
            lambda: control("sync.status", query)["mutagen"]["conflicts"] == "detected",
            "actual Mutagen conflict",
        )
        assert (project / "from-local.txt").read_text() == "local conflicting edit\n"
        assert remote_read("from-local.txt") == "remote conflicting edit\n"
        print("PASS pause/resume and two-way-safe conflict preservation", flush=True)
        # Resolution follows an explicit manual edit, never a fixture-side reset
        # that silently chooses a winning side of a real conflict.
        remote_write("from-local.txt", "local conflicting edit\n")
        action("resolve")
        assert control("sync.status", query)["mutagen"]["conflicts"] == "none_observed"
        action("reset")
        assert remote_read("from-local.txt") == "local conflicting edit\n"
        assert (project / "from-local.txt").read_text() == "local conflicting edit\n"
        print(
            "PASS explicit manual conflict resolution and confirmed reset", flush=True
        )

        ssh = [
            str(root / "system-ssh"),
            "-o",
            "StrictHostKeyChecking=yes",
            "-o",
            "UserKnownHostsFile=" + str(root / "home/ssh/known_hosts"),
            "-p",
            str(ssh_port),
            "agent-remote@127.0.0.1",
            "printf sync-authorized",
        ]
        assert run(ssh).stdout == "sync-authorized"
        stop_poll.set()
        poll_thread.join(95)
        assert not poll_thread.is_alive()
        authorized_before = docker(
            "cat", "/var/lib/agent-remote-node/authorized_keys"
        ).stdout
        invalid_entry, count = re.subn(
            r"--ssh-key [0-9a-f-]{36}", "--ssh-key " + str(uuid4()), authorized_before
        )
        assert count == 1
        docker(
            "runuser",
            "-u",
            "agent-remote",
            "--",
            "python3",
            "-c",
            "import pathlib,sys;pathlib.Path('/var/lib/agent-remote-node/authorized_keys').write_text(sys.stdin.read())",
            data=invalid_entry,
        )
        invalid = run(ssh, check=False)
        assert invalid.returncode != 0 and "sync-authorized" not in invalid.stdout
        assert "HTTP 403 (SSH_KEY_REVOKED)" in invalid.stderr
        docker(
            "runuser",
            "-u",
            "agent-remote",
            "--",
            "python3",
            "-c",
            "import pathlib,sys;pathlib.Path('/var/lib/agent-remote-node/authorized_keys').write_text(sys.stdin.read())",
            data=authorized_before,
        )
        assert run(ssh).stdout == "sync-authorized"
        print("PASS unknown forced-command SSH key identity rejected", flush=True)

        async def revoke():
            """Model control-plane key revocation while the old authorized_keys entry remains."""
            async with app.state.session_factory() as session:
                ssh_key = await session.scalar(
                    select(SshKey).where(
                        SshKey.user_device_id == UUID(context["active_device_id"])
                    )
                )
                assert ssh_key is not None
                ssh_key.status = "revoked"
                await session.commit()

        asyncio.run(revoke())
        denied = run(ssh, check=False)
        assert denied.returncode != 0 and "sync-authorized" not in denied.stdout
        assert "HTTP 403 (SSH_KEY_REVOKED)" in denied.stderr
        assert (
            docker("cat", "/var/lib/agent-remote-node/authorized_keys").stdout
            == authorized_before
        )
        print(
            "PASS revoked exact SSH key rejected despite stale authorized_keys",
            flush=True,
        )
    except Exception:
        # These contain only synthetic fixture identities and temporary paths.
        mutagen = root / "home/bin/mutagen"
        if mutagen.exists():
            observed = run(
                [str(mutagen), "sync", "list", "--long"],
                env={**environment, "MUTAGEN_DISABLE_AUTOSTART": "1"},
                check=False,
                timeout=10,
            )
            print(
                "Fixture Mutagen observation:",
                observed.stdout[-3000:],
                observed.stderr[-1000:],
                flush=True,
            )
        raise
    finally:
        stop_poll.set()
        if poll_thread is not None:
            poll_thread.join(95)
        mutagen = root / "home/bin/mutagen"
        if mutagen.exists():
            run(
                [str(mutagen), "daemon", "stop"],
                env=environment,
                timeout=20,
                check=False,
            )
        run(["docker", "rm", "--force", container], check=False, timeout=30)
        client.close()
        server.should_exit = True
        server_thread.join(20)
        listener.close()


if __name__ == "__main__":
    main()
