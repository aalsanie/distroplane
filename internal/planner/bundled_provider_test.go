package planner

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func bundledResolver(t *testing.T) (executableResolver, string, string, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	libexec := filepath.Join(root, "libexec")
	shadow := filepath.Join(root, "shadow")
	for _, path := range []string{bin, libexec, shadow} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	cli := filepath.Join(bin, "distroplane"+ext)
	if err := os.WriteFile(cli, []byte("cli"), 0700); err != nil {
		t.Fatal(err)
	}
	resolver := newExecutableResolver()
	resolver.executable = func() (string, error) { return cli, nil }
	return resolver, libexec, shadow, ext
}

func fixtureProvider(t *testing.T, dir, name, ext string) string {
	t.Helper()
	path := filepath.Join(dir, providerExecutablePrefix+name+ext)
	if err := os.WriteFile(path, []byte(name), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBundledProviderPrecedesShadowedPath(t *testing.T) {
	r, trusted, shadow, ext := bundledResolver(t)
	want := fixtureProvider(t, trusted, "npm", ext)
	fixtureProvider(t, shadow, "npm", ext)
	r.lookPath = func(name string) (string, error) {
		t.Fatalf("PATH was consulted despite valid installed provider: %s", name)
		return "", errors.New("unsafe PATH")
	}
	got, err := r.Resolve("npm", "")
	if err != nil || got != want {
		t.Fatalf("bundled provider: got=%q, want=%q, err=%v", got, want, err)
	}
}

func TestExplicitProviderOverridesBundledAndPath(t *testing.T) {
	r, trusted, shadow, ext := bundledResolver(t)
	fixtureProvider(t, trusted, "npm", ext)
	want := fixtureProvider(t, shadow, "npm", ext)
	r.lookPath = func(name string) (string, error) {
		if name == "custom-npm" {
			return want, nil
		}
		t.Fatalf("unexpected PATH lookup: %s", name)
		return "", errors.New("unexpected")
	}
	if got, err := r.Resolve("npm", want); err != nil || got != want {
		t.Fatalf("explicit path lost precedence: got=%q err=%v", got, err)
	}
	if got, err := r.Resolve("npm", "custom-npm"); err != nil || got != want {
		t.Fatalf("explicit PATH command lost precedence: got=%q err=%v", got, err)
	}
	if _, err := r.Resolve("npm", filepath.Join(shadow, "missing")); err == nil {
		t.Fatal("missing explicitly configured executable fell back to bundle")
	}
}

func TestDefaultProviderUsesPathWhenNoBundleExists(t *testing.T) {
	r, _, shadow, ext := bundledResolver(t)
	want := fixtureProvider(t, shadow, "custom", ext)
	calls := 0
	r.lookPath = func(candidate string) (string, error) {
		calls++
		if candidate != providerExecutablePrefix+"custom"+ext {
			t.Fatalf("bad PATH executable: %s", candidate)
		}
		return want, nil
	}
	if got, err := r.Resolve("custom", ""); err != nil || got != want || calls != 1 {
		t.Fatalf("PATH fallback: got=%q err=%v calls=%d", got, err, calls)
	}
}

func TestInvalidInstalledProviderFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		bad func(*testing.T, string, string)
	}{
		{"directory", func(t *testing.T, dir, path string) {
			if err := os.Mkdir(path, 0755); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink", func(t *testing.T, dir, path string) {
			target := filepath.Join(dir, "outside")
			if err := os.WriteFile(target, []byte("binary"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
		{"not-executable", func(t *testing.T, dir, path string) {
			if runtime.GOOS == "windows" {
				t.Skip("Unix executable bit is not applicable to Windows")
			}
			if err := os.WriteFile(path, []byte("binary"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"world-writable", func(t *testing.T, dir, path string) {
			if runtime.GOOS == "windows" {
				t.Skip("POSIX file permissions not applicable to Windows")
			}
			if err := os.WriteFile(path, []byte("binary"), 0777); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, dir, shadow, ext := bundledResolver(t)
			fixtureProvider(t, shadow, "npm", ext)
			tc.bad(t, dir, filepath.Join(dir, providerExecutablePrefix+"npm"+ext))
			r.lookPath = func(name string) (string, error) {
				t.Fatalf("unsafe installed provider fell back to PATH: %s", name)
				return "", errors.New("unsafe fallback")
			}
			if _, err := r.Resolve("npm", ""); err == nil {
				t.Fatal("unsafe installed provider accepted")
			}
		})
	}
}

func TestUntrustedBundleDirectoryIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permissions not applicable to Windows")
	}
	r, dir, _, ext := bundledResolver(t)
	fixtureProvider(t, dir, "npm", ext)
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("npm", ""); err == nil {
		t.Fatal("writable installation directory accepted")
	}
}

func TestBundledExecutableResolveErrorsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		override func(*executableResolver)
	}{
		{"os-executable", func(r *executableResolver) {
			r.executable = func() (string, error) { return "", errors.New("executable unknown") }
		}},
		{"canonical-cli", func(r *executableResolver) {
			r.evalLinks = func(string) (string, error) { return "", errors.New("invalid executable symlink") }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _, _ := bundledResolver(t)
			tc.override(&r)
			if _, err := r.Resolve("npm", ""); err == nil {
				t.Fatal("installation discovery error ignored")
			}
		})
	}
}

func TestUnrelatedExecutableDoesNotBecomeTrustedRoot(t *testing.T) {
	r, trusted, shadow, ext := bundledResolver(t)
	fixtureProvider(t, trusted, "npm", ext)
	want := fixtureProvider(t, shadow, "npm", ext)
	unrelated := filepath.Join(filepath.Dir(trusted), "some-other-tool"+ext)
	if err := os.WriteFile(unrelated, []byte("other tool"), 0700); err != nil {
		t.Fatal(err)
	}
	r.executable = func() (string, error) { return unrelated, nil }
	r.lookPath = func(string) (string, error) { return want, nil }
	if got, err := r.Resolve("npm", ""); err != nil || got != want {
		t.Fatalf("unrelated process incorrectly trusted installation: %s %v", got, err)
	}
}

func TestProviderNameCannotEscapeTrustedDirectory(t *testing.T) {
	r, _, _, _ := bundledResolver(t)
	for _, name := range []string{"", "../npm", "npm/other", "npm\\other", "-npm", ".", strings.Repeat("n", 129)} {
		if _, err := r.Resolve(name, ""); err == nil {
			t.Fatalf("unsafe provider name %q accepted", name)
		}
	}
}

func TestSideBySideProviderInstallation(t *testing.T) {
	r, dir, _, ext := bundledResolver(t)
	cli, err := r.executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Dir(cli)
	name := "sdkman"
	want := fixtureProvider(t, bin, name, ext)
	if got, err := r.Resolve(name, ""); err != nil || got != want {
		t.Fatalf("side-by-side provider failed: %q %v", got, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
