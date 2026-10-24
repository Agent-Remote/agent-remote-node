#!/bin/sh
# Runs exclusively inside the disposable test container; never on the host.
set -eu
test -f /.dockerenv
export DEBIAN_FRONTEND=noninteractive
apt-get -o Acquire::http::Timeout=30 -o Acquire::Retries=2 update -qq
apt-get -o Acquire::http::Timeout=30 -o Acquire::Retries=2 install -y -qq \
  --no-install-recommends openssh-server sudo bubblewrap acl python3 ca-certificates >/dev/null
useradd --create-home --shell /bin/sh agent-remote
# Public-key-only sshd needs an unlocked account; no usable password is installed.
usermod --password '*' agent-remote
mkdir -p /run/sshd /run/agent-remote /etc/agent-remote-node /var/lib/agent-remote-node
chown root:agent-remote /run/agent-remote
chmod 0750 /run/agent-remote
chown agent-remote:agent-remote /var/lib/agent-remote-node
cp /proof/agent-remote-node /proof/agent-remote-attach /proof/agent-remote-runtime /usr/local/bin/
chmod 0755 /usr/local/bin/agent-remote-*
printf 'agent-remote ALL=(root) NOPASSWD: /usr/local/bin/agent-remote-runtime sync-command *\n' > /etc/sudoers.d/agent-remote-sync-proof
chmod 0440 /etc/sudoers.d/agent-remote-sync-proof
cat > /etc/ssh/sshd_config <<'EOF'
Port 22
ListenAddress 0.0.0.0
HostKey /etc/ssh/ssh_host_ed25519_key
AuthorizedKeysFile /var/lib/agent-remote-node/authorized_keys
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
UsePAM no
AllowUsers agent-remote
AllowAgentForwarding no
AllowTcpForwarding no
X11Forwarding no
PermitUserEnvironment no
StrictModes yes
PidFile /run/sshd.pid
EOF
touch /tmp/sync-proof-ready
exec sleep infinity
