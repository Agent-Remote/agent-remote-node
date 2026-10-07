package runtimehelper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestNativeSystemFiles(t *testing.T) {
	if os.Getenv("AGENT_REMOTE_RUN_NATIVE_FILES_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated Linux container with bubblewrap and CAP_SYS_ADMIN")
	}
	root := t.TempDir()
	for _, path := range []string{filepath.Dir(root), root} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	spec := SessionSpec{
		SessionRoot: filepath.Join(root, "session"), RuntimeRoot: filepath.Join(root, "runtime"),
		WorkspacePath: filepath.Join(root, "workspace"), AccountPath: filepath.Join(root, "account"),
		Timezone: "UTC", Locale: "C", RuntimeCommand: "/bin/sh",
	}
	for _, path := range []string{spec.SessionRoot, spec.RuntimeRoot, spec.WorkspacePath, filepath.Join(spec.AccountPath, ".claude"), filepath.Join(spec.SessionRoot, "tmp")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(filepath.Join(spec.SessionRoot, "tmp"), 12345, 12345); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(filepath.Join(spec.AccountPath, ".claude"), 12345, 12345); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(spec.SessionRoot, "passwd"):       "runtime:x:12345:12345::/home/runtime:/bin/sh\n",
		filepath.Join(spec.SessionRoot, "group"):        "runtime:x:12345:\n",
		filepath.Join(spec.SessionRoot, "timezone"):     "UTC\n",
		filepath.Join(spec.SessionRoot, "resolv.conf"):  "nameserver 192.0.2.1\n",
		filepath.Join(spec.AccountPath, ".claude.json"): "{}\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec.Argv = []string{"-eu", "-c", `
test "$(id -u)" = 12345
test "$(awk 'BEGIN { print 42 }')" = 42
printf 'int main(void) { return 0; }\n' > /tmp/hello.c
cc /tmp/hello.c -o /tmp/hello
/tmp/hello
openssl version
openssl req -new -newkey rsa:2048 -nodes -subj /CN=native-test -keyout /tmp/key.pem -out /tmp/request.pem 2>/dev/null
cat > /tmp/NativeProof.java <<'JAVA'
public class NativeProof {
    public static void main(String[] args) throws Exception {
        if (java.security.Security.getProviders().length == 0) throw new AssertionError();
        javax.net.ssl.SSLContext.getDefault().createSSLEngine();
    }
}
JAVA
javac /tmp/NativeProof.java
java -cp /tmp NativeProof
mvn --version
python3 - <<'PY'
import glob, mimetypes, multiprocessing, os, platform, pwd, socket, ssl
semaphore = multiprocessing.Semaphore(1)
assert semaphore.acquire(timeout=1)
semaphore.release()
assert socket.gethostbyname('native-hosts-proof.invalid') == '192.0.2.42'
assert socket.gethostbyname('localhost') == '127.0.0.1'
assert socket.getaddrinfo('localhost', None, socket.AF_INET6)[0][4][0] == '::1'
assert socket.getservbyname('https', 'tcp') == 443
assert socket.getprotobyname('tcp') == 6
assert platform.freedesktop_os_release()['ID'] in ('debian', 'ubuntu')
context = ssl.create_default_context()
verify_paths = ssl.get_default_verify_paths()
cafile = verify_paths.cafile
if cafile is None and verify_paths.capath:
    # OpenSSL loads capath lazily; check that a default hashed root is readable.
    roots = glob.glob(os.path.join(verify_paths.capath, '[0-9a-f]' * 8 + '.[0-9]'))
    cafile = next((path for path in roots if os.path.isfile(path)), None)
assert cafile is not None, verify_paths
context.load_verify_locations(cafile=cafile)
assert context.cert_store_stats()['x509_ca'] > 0
mimetypes.init(['/etc/mime.types'])
assert mimetypes.guess_type('test.pdf')[0] == 'application/pdf'
assert pwd.getpwuid(os.getuid()).pw_name == 'runtime'
assert len(pwd.getpwall()) == 1
assert open('/etc/resolv.conf').read() == 'nameserver 192.0.2.1\n'
assert os.path.realpath('/etc/mtab') == '/proc/%d/mounts' % os.getpid()
assert os.path.realpath('/var/run') == '/run'
with open('/var/tmp/native-temp-proof', 'w') as f:
    f.write('session temporary storage')
assert open('/tmp/native-temp-proof').read() == 'session temporary storage'
mounts = {line.split()[1]: line.split()[3].split(',') for line in open('/proc/self/mounts')}
for path in ['/etc/hosts', '/etc/services', '/etc/protocols', '/etc/alternatives', '/etc/ld.so.cache', '/etc/os-release', '/etc/ssl']:
    assert 'ro' in mounts[path], (path, mounts.get(path))
for path in ['/etc/shadow', '/etc/ssh', '/etc/agent-remote-native-test-secret', '/etc/maven/settings.xml', '/etc/maven/toolchains.xml']:
    assert not os.path.exists(path), path
assert not glob.glob('/etc/java-*-openjdk/management/*')
PY
`}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bwrap", bubblewrapArgs(EngineConfig{}, spec)...)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 12345, Gid: 12345}}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native system file proof failed: %s: %v", output, err)
	}
}
