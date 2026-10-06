package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The production compose template once mounted ./stacks:/opt/stacks while
// STACKS_DIR and HOST_STACKS_DIR were both /opt/stacks, so the old
// string-only check logged "verified" while relative binds in managed stacks
// resolved to the wrong host path (agent-os-a1ye.1). These fixtures are real
// /proc/self/mountinfo lines, captured on a host whose /home is its own
// partition (/dev/nvme0n1p5) and whose / is /dev/nvme0n1p2. Field 4 is the
// path relative to the SOURCE FILESYSTEM's root, not the host path: that is
// why the /home prefix is missing from the partition lines.
const (
	// docker run --rm -v /home/edwin/.cache/a1ye-probe/stacks:/opt/stacks alpine cat /proc/self/mountinfo
	mountinfoOldTemplate = `745 727 259:5 /edwin/.cache/a1ye-probe/stacks /opt/stacks rw,relatime - ext4 /dev/nvme0n1p5 rw,errors=remount-ro,stripe=32`
	// docker run --rm -v /var/tmp/a1ye-stacks:/var/tmp/a1ye-stacks alpine cat /proc/self/mountinfo
	mountinfoRootFSIdentical = `745 724 259:2 /var/tmp/a1ye-stacks /var/tmp/a1ye-stacks rw,relatime - ext4 /dev/nvme0n1p2 rw,errors=remount-ro,stripe=32`
	// docker run --rm -v /home/edwin/.cache/a1ye-probe/stacks:/home/edwin/.cache/a1ye-probe/stacks alpine cat /proc/self/mountinfo
	mountinfoSeparatePartition = `745 724 259:5 /edwin/.cache/a1ye-probe/stacks /home/edwin/.cache/a1ye-probe/stacks rw,relatime - ext4 /dev/nvme0n1p5 rw,errors=remount-ro,stripe=32`
	// docker run --rm -v /home/edwin/.cache/a1ye-probe:/home/edwin/.cache/a1ye-probe alpine cat /proc/self/mountinfo
	mountinfoAncestor = `745 724 259:5 /edwin/.cache/a1ye-probe /home/edwin/.cache/a1ye-probe rw,relatime - ext4 /dev/nvme0n1p5 rw,errors=remount-ro,stripe=32`
	// docker run --rm -v "/home/edwin/.cache/a1ye-probe/my stacks:/opt/my stacks" alpine cat /proc/self/mountinfo
	mountinfoEscapedSpace = `741 724 259:5 /edwin/.cache/a1ye-probe/my\040stacks /opt/my\040stacks rw,relatime - ext4 /dev/nvme0n1p5 rw,errors=remount-ro,stripe=32`
	// docker run --rm alpine cat /proc/self/mountinfo | head -3 (no volumes: the overlay root only)
	mountinfoNoVolume = `727 135 0:185 / / rw,relatime - overlay overlay rw,lowerdir=/var/lib/docker/overlay2/l/QPIB3KKAGMQSESBJWJZUKTZTYA
728 727 0:202 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
729 727 0:204 / /dev rw,nosuid - tmpfs tmpfs rw,size=65536k,mode=755,inode64`
)

func TestInspectStacksMount(t *testing.T) {
	cases := []struct {
		name      string
		stacksDir string
		mountinfo string
		want      stacksMountVerdict
	}{
		{"old template mounts a different host dir", "/opt/stacks", mountinfoNoVolume + "\n" + mountinfoOldTemplate, stacksMountMismatch},
		{"identical path on the root filesystem", "/var/tmp/a1ye-stacks", mountinfoNoVolume + "\n" + mountinfoRootFSIdentical, stacksMountVerified},
		{"identical path on a separate partition", "/home/edwin/.cache/a1ye-probe/stacks", mountinfoSeparatePartition, stacksMountConsistent},
		{"STACKS_DIR below the mount point", "/home/edwin/.cache/a1ye-probe/stacks", mountinfoAncestor, stacksMountConsistent},
		{"STACKS_DIR below the mount point, wrong container path", "/srv/stacks", mountinfoAncestor, stacksMountNotInspected},
		{"escaped space, different host dir", "/opt/my stacks", mountinfoEscapedSpace, stacksMountMismatch},
		{"no bind mount, overlay root only", "/opt/stacks", mountinfoNoVolume, stacksMountNotInspected},
		{"mountinfo unreadable", "/opt/stacks", "", stacksMountNotInspected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := inspectStacksMount(tc.stacksDir, tc.mountinfo)
			if got != tc.want {
				t.Errorf("inspectStacksMount(%q) = %d, want %d", tc.stacksDir, got, tc.want)
			}
		})
	}
}

func useMountinfoFixture(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := mountinfoPath
	mountinfoPath = path
	t.Cleanup(func() { mountinfoPath = prev })
}

func TestValidateVolumePathIdentity_Logging(t *testing.T) {
	cases := []struct {
		name          string
		stacksDir     string
		hostStacksDir string
		mountinfo     string
		wantLevel     string
		wantText      string
	}{
		{"mismatch is an error naming both paths", "/opt/stacks", "/opt/stacks", mountinfoOldTemplate, "level=ERROR", "/edwin/.cache/a1ye-probe/stacks"},
		{"exact match is verified", "/var/tmp/a1ye-stacks", "/var/tmp/a1ye-stacks", mountinfoRootFSIdentical, "level=INFO", "verified"},
		{"separate partition is consistent, not proven", "/home/edwin/.cache/a1ye-probe/stacks", "/home/edwin/.cache/a1ye-probe/stacks", mountinfoSeparatePartition, "level=INFO", "consistent"},
		{"no mount entry falls back to the string compare", "/opt/stacks", "/srv/stacks", mountinfoNoVolume, "level=WARN", "mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useMountinfoFixture(t, tc.mountinfo)
			buf := captureSlog(t)
			validateVolumePathIdentity(&Config{StacksDir: tc.stacksDir, HostStacksDir: tc.hostStacksDir})
			out := buf.String()
			var line string
			for _, l := range strings.Split(out, "\n") {
				if strings.Contains(l, "Volume path identity") {
					line = l
				}
			}
			if !strings.Contains(line, tc.wantLevel) || !strings.Contains(line, tc.wantText) {
				t.Errorf("want a %s line containing %q, got log output:\n%s", tc.wantLevel, tc.wantText, out)
			}
		})
	}
}
