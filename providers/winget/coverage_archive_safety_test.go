package winget

import "testing"

func TestZIPManifestRejectsEveryWindowsReservedDeviceAlias(t *testing.T) {
	for _, name := range []string{
		"CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
	} {
		if !reservedWindowsName(name + ".exe") {
			t.Errorf("Windows reserved device %s accepted", name)
		}
		if err := validateArchiveMemberPath("bin/" + name + ".exe"); err == nil {
			t.Errorf("unsafe archived Windows device name %s accepted", name)
		}
	}
	for _, name := range []string{"APP.exe", "COM0.exe", "COM10.exe", "LPT0.exe", "LPT10.exe"} {
		if reservedWindowsName(name) {
			t.Errorf("ordinary archive filename was blocked: %s", name)
		}
	}
	for _, path := range []string{"", "/bin/a.exe", "../a.exe", "bin/../a.exe", "bin/a\\b.exe", "a:stream", "a?.exe", "bin/a.", "bin/a ", "bin//a.exe"} {
		if err := validateArchiveMemberPath(path); err == nil {
			t.Errorf("unsafe ZIP member path accepted: %q", path)
		}
	}
	for _, path := range []string{"bin/app.exe", "app.msi", "bin/subdir/app.exe"} {
		if err := validateArchiveMemberPath(path); err != nil {
			t.Errorf("safe ZIP member path rejected: %q: %v", path, err)
		}
	}
	for _, alias := range []string{"App", "app2", "app-launcher", "My_App", "hello.world"} {
		if !validPortableAlias(alias) {
			t.Errorf("safe portable launcher alias rejected: %q", alias)
		}
	}
	for _, alias := range []string{"", "2app", "CON", "app/path", "bad alias", "bad\\alias"} {
		if validPortableAlias(alias) {
			t.Errorf("unsafe portable launcher alias accepted: %q", alias)
		}
	}
}
