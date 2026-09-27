#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_case="${AGENT_REMOTE_KERNEL_TEST_CASE:-session}"
[[ "$test_case" == session || "$test_case" == migration ]]
host_disk="${AGENT_REMOTE_KERNEL_TEST_DISK_ON_HOST:-0}"
[[ "$host_disk" == 0 || "$host_disk" == 1 ]]
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-kernel-reboot.XXXXXX")"
test_tag="$(basename "$test_root")"
test_image="agent-remote-kernel-reboot:$test_tag"
cleanup() {
  docker rm -f "$test_tag" >/dev/null 2>&1 || true
  docker image rm "$test_image" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT
docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  arm64 | aarch64) runner_arch=arm64 ;;
  amd64 | x86_64) runner_arch=amd64 ;;
  *) echo 'Unsupported Docker architecture' >&2; exit 1 ;;
esac
test_arch="${AGENT_REMOTE_KERNEL_TEST_ARCH:-$runner_arch}"
case "$test_arch" in
  arm64 | aarch64) go_arch=arm64; kernel_package=linux-image-arm64; qemu_package=qemu-system-arm ;;
  amd64 | x86_64) go_arch=amd64; kernel_package=linux-image-amd64; qemu_package=qemu-system-x86 ;;
  *) echo 'Unsupported kernel test architecture: use arm64 or amd64' >&2; exit 1 ;;
esac
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go test -c -o "$test_root/runtimehelper.test" ./internal/runtimehelper
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go build -o "$test_root/agent-remote-runtime" ./cmd/agent-remote-runtime
)
# Only public trust roots enter the package build; no user login material is needed.
public_ca="${AGENT_REMOTE_TEST_PUBLIC_CA:-/etc/ssl/cert.pem}"
if [[ ! -f "$public_ca" ]]; then public_ca=/etc/ssl/certs/ca-certificates.crt; fi
cp -- "$public_ca" "$test_root/public-ca.crt"
cat > "$test_root/Dockerfile" <<'DOCKERFILE'
ARG GUEST_ARCH
FROM --platform=linux/$GUEST_ARCH debian:bookworm-slim AS guest
ARG KERNEL_PACKAGE
ARG QEMU_PACKAGE
ENV DEBIAN_FRONTEND=noninteractive
COPY public-ca.crt /proof/public-ca.crt
RUN sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && apt-get -o APT::Update::Error-Mode=any -o Acquire::https::CaInfo=/proof/public-ca.crt -o Acquire::https::Timeout=30 update -qq && apt-get -o Acquire::https::CaInfo=/proof/public-ca.crt -o Acquire::https::Timeout=30 install -y -qq --no-install-recommends systemd systemd-sysv dbus tmux bubblewrap iproute2 nftables acl passwd e2fsprogs initramfs-tools "$KERNEL_PACKAGE" "$QEMU_PACKAGE" && rm -rf /var/lib/apt/lists/*
COPY --chmod=755 runtimehelper.test agent-remote-runtime boot-test.sh test-case /proof/
FROM --platform=$BUILDPLATFORM debian:bookworm-slim
ARG QEMU_PACKAGE
ARG GUEST_ARCH
ENV TEST_GUEST_ARCH=$GUEST_ARCH
ENV DEBIAN_FRONTEND=noninteractive
COPY public-ca.crt /proof/public-ca.crt
RUN sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && apt-get -o APT::Update::Error-Mode=any -o Acquire::https::CaInfo=/proof/public-ca.crt -o Acquire::https::Timeout=30 update -qq && apt-get -o Acquire::https::CaInfo=/proof/public-ca.crt -o Acquire::https::Timeout=30 install -y -qq --no-install-recommends e2fsprogs "$QEMU_PACKAGE" && rm -rf /var/lib/apt/lists/*
COPY --from=guest / /guest/
COPY --chmod=755 run-vm.sh /proof/
ENTRYPOINT ["/proof/run-vm.sh"]
DOCKERFILE
cat > "$test_root/boot-test.sh" <<'BOOT'
#!/bin/bash
set -eu
export AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST=1
if test -f /var/lib/ar-kernel-reboot.json; then
  export AGENT_REMOTE_SKILL_KERNEL_REBOOT_PHASE=recover
else
  export AGENT_REMOTE_SKILL_KERNEL_REBOOT_PHASE=seed
fi
case "$(cat /proof/test-case)" in
  session) filter='^TestManagedSkillActualKernelReboot$' ;;
  migration) filter='^TestMigrationActualKernelReboot$' ;;
  *) exit 1 ;;
esac
/proof/runtimehelper.test -test.v -test.timeout=5m -test.run "$filter" || echo AR_KERNEL_REBOOT_FAILED
sync
systemctl poweroff --force
BOOT
cat > "$test_root/run-vm.sh" <<'VM'
#!/bin/bash
set -euo pipefail
mkdir -p /vm/rootfs
for tree in bin sbin lib lib64 usr etc var boot root opt home proof; do
  if test -e "/guest/$tree"; then cp -a "/guest/$tree" /vm/rootfs/; fi
done
mkdir -p /vm/rootfs/{dev,proc,sys,run,tmp}
chmod 1777 /vm/rootfs/tmp
truncate -s 0 /vm/rootfs/etc/machine-id
printf '/dev/vda / ext4 defaults 0 1\n' > /vm/rootfs/etc/fstab
cat > /vm/rootfs/etc/systemd/system/ar-reboot-proof.service <<'UNIT'
[Unit]
Description=Isolated managed skill reboot proof
After=systemd-tmpfiles-setup.service dbus.service
[Service]
Type=oneshot
ExecStart=/proof/boot-test.sh
StandardOutput=journal+console
StandardError=journal+console
TimeoutStartSec=300
[Install]
WantedBy=multi-user.target
UNIT
mkdir -p /vm/rootfs/etc/systemd/system/multi-user.target.wants
ln -s ../ar-reboot-proof.service /vm/rootfs/etc/systemd/system/multi-user.target.wants/ar-reboot-proof.service
disk="${TEST_VM_DISK:-/vm/disk.raw}"
truncate -s 6G "$disk"
mkfs.ext4 -q -F -d /vm/rootfs "$disk"
kernel="$(find /guest/boot -maxdepth 1 -name 'vmlinuz-*' | sort | tail -1)"
initrd="$(find /guest/boot -maxdepth 1 -name 'initrd.img-*' | sort | tail -1)"
case "$TEST_GUEST_ARCH" in
  arm64) qemu=(qemu-system-aarch64 -machine virt -cpu max); console=ttyAMA0; rng_device=virtio-rng-device ;;
  amd64) qemu=(qemu-system-x86_64 -machine q35 -cpu max); console=ttyS0; rng_device=virtio-rng-pci ;;
  *) exit 1 ;;
esac
qemu+=(-accel tcg,thread=multi -smp 2 -m 1536 -nographic -no-reboot -nic none
  -object rng-random,filename=/dev/urandom,id=rng0 -device "$rng_device,rng=rng0"
  -kernel "$kernel" -initrd "$initrd" -append "root=/dev/vda rw rootwait console=$console systemd.unit=multi-user.target"
  -drive "file=$disk,format=raw,if=virtio,cache=none")
vm_pid=''
trap 'if [[ -n "$vm_pid" ]]; then kill -KILL "$vm_pid" 2>/dev/null || true; wait "$vm_pid" 2>/dev/null || true; fi' EXIT
: > /vm/seed.log
"${qemu[@]}" > /vm/seed.log 2>&1 &
vm_pid=$!
seeded=false
for attempt in $(seq 1 240); do
  if grep -q AR_KERNEL_REBOOT_SEEDED /vm/seed.log; then seeded=true; break; fi
  if ! kill -0 "$vm_pid" 2>/dev/null; then break; fi
  sleep 1
done
if [[ "$seeded" != true ]]; then tail -160 /vm/seed.log; exit 1; fi
grep AR_KERNEL_REBOOT_SEEDED /vm/seed.log
kill -KILL "$vm_pid"
wait "$vm_pid" 2>/dev/null || true
vm_pid=''
if ! timeout --signal=TERM --kill-after=5 240 "${qemu[@]}" > /vm/recover.log 2>&1; then tail -160 /vm/recover.log; exit 1; fi
if ! grep -q AR_KERNEL_REBOOT_RECOVERED /vm/recover.log || grep -q AR_KERNEL_REBOOT_FAILED /vm/recover.log; then tail -160 /vm/recover.log; exit 1; fi
grep -E 'AR_KERNEL_REBOOT_RECOVERED|PASS' /vm/recover.log
VM
printf '%s\n' "$test_case" > "$test_root/test-case"
docker build --quiet --platform "linux/$runner_arch" --build-arg "GUEST_ARCH=$go_arch" --build-arg "KERNEL_PACKAGE=$kernel_package" --build-arg "QEMU_PACKAGE=$qemu_package" --tag "$test_image" "$test_root" >/dev/null
# TCG needs neither host devices nor a privileged container; both kernels use only this disposable disk.
# QEMU itself runs natively even when the guest architecture differs from the Docker host.
run_args=(--rm --platform "linux/$runner_arch" --name "$test_tag")
if [[ "$host_disk" == 1 ]]; then
  # The same private raw VM filesystem can use host free space instead of Docker's data disk.
  : > "$test_root/disk.raw"
  chmod 600 "$test_root/disk.raw"
  run_args+=(--mount "type=bind,src=$test_root/disk.raw,dst=/vm-test-disk.raw" --env TEST_VM_DISK=/vm-test-disk.raw)
fi
docker run "${run_args[@]}" "$test_image"
