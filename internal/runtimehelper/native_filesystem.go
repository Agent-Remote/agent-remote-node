package runtimehelper

import "path/filepath"

// appendNativeSystemMounts exposes the public system data used by the mounted
// host toolchain. Keep an explicit allowlist: /etc also contains host secrets.
func appendNativeSystemMounts(args []string) []string {
	for _, path := range []string{
		"/usr", "/bin", "/lib", "/lib64",
		// NSS, address selection, and service/protocol name databases.
		"/etc/hosts", "/etc/hostname", "/etc/host.conf", "/etc/nsswitch.conf", "/etc/gai.conf",
		"/etc/services", "/etc/protocols", "/etc/networks",
		// Debian/Ubuntu commands such as awk and cc resolve through alternatives.
		"/etc/alternatives", "/etc/ld.so.cache", "/etc/ld.so.conf", "/etc/ld.so.conf.d",
		"/etc/os-release", "/etc/debian_version", "/etc/lsb-release",
		"/etc/mime.types", "/etc/terminfo", "/etc/inputrc",
		"/etc/ssl", "/etc/pki",
		"/etc/maven/m2.conf", "/etc/maven/logging",
	} {
		if pathExists(path) {
			args = append(args, "--ro-bind", path, path)
		}
	}
	// Packaged OpenJDK binaries link their runtime data back into /etc. Bind only
	// public configuration, excluding management passwords and Maven settings.
	for _, pattern := range []string{
		"/etc/java-*-openjdk/jvm-*.cfg",
		"/etc/java-*-openjdk/*.properties", "/etc/java-*-openjdk/psfont.properties.ja",
		"/etc/java-*-openjdk/jfr",
		"/etc/java-*-openjdk/security/java.security", "/etc/java-*-openjdk/security/java.policy",
		"/etc/java-*-openjdk/security/default.policy", "/etc/java-*-openjdk/security/nss.cfg",
		"/etc/java-*-openjdk/security/blocked.certs", "/etc/java-*-openjdk/security/public_suffix_list.dat",
		"/etc/java-*-openjdk/security/policy",
	} {
		paths, _ := filepath.Glob(pattern)
		for _, path := range paths {
			if pathExists(path) {
				args = append(args, "--ro-bind", path, path)
			}
		}
	}
	// Mount information must describe this namespace, and conventional temporary
	// paths must use the session's quota-limited /tmp rather than host storage.
	return append(args,
		"--symlink", "/proc/self/mounts", "/etc/mtab",
		"--dir", "/var",
		"--symlink", "/tmp", "/var/tmp",
		"--symlink", "/run", "/var/run",
	)
}
