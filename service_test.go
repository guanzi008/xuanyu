package main

import (
	"strings"
	"testing"
)

func testInstallOptions() installOptions {
	return installOptions{
		baseURL:                "https://hs.example.com",
		domain:                 "hs.example.com",
		listen:                 "127.0.0.1:8080",
		dataDir:                "/var/lib/headscale-console",
		configPath:             "/etc/headscale-console/config.yaml",
		adminPath:              "/admin",
		baseDomain:             "tailnet.example.com",
		registration:           true,
		allowedEmailDomains:    []string{"example.com"},
		passwordMinLength:      12,
		defaultRole:            "member",
		derpURLs:               []string{defaultDERPURL},
		dnsNameservers:         []string{"1.1.1.1", "1.0.0.1"},
		idpListen:              defaultIDPListen,
		headplaneListen:        defaultHeadplaneListen,
		headscaleHTTPListen:    defaultHeadscaleHTTP,
		headscaleGRPCListen:    defaultHeadscaleGRPC,
		headscaleMetricsListen: defaultHeadscaleMetric,
	}
}

func TestRenderConfig(t *testing.T) {
	content := renderConfig(testInstallOptions())
	for _, expected := range []string{
		`base_url: "https://hs.example.com"`,
		`admin_path: "/admin"`,
		`idp: "127.0.0.1:18070"`,
		`enabled: true`,
		`    - "example.com"`,
		`password_min_length: 12`,
		`default_role: "member"`,
		`base_domain: "tailnet.example.com"`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("config missing %q:\n%s", expected, content)
		}
	}
}

func TestRenderServiceUnit(t *testing.T) {
	unit := renderServiceUnit(testInstallOptions())
	for _, expected := range []string{
		`ExecStart="/opt/headscale-console/headscale-console" "--config" "/etc/headscale-console/config.yaml" "--data-dir" "/var/lib/headscale-console"`,
		`User=headscale-console`,
		`ProtectSystem=strict`,
		`ReadWritePaths=/var/lib/headscale-console`,
		`Restart=on-failure`,
	} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("unit missing %q:\n%s", expected, unit)
		}
	}
}

func TestValidateBaseURL(t *testing.T) {
	for _, valid := range []string{"https://hs.example.com", "http://127.0.0.1:8080"} {
		if err := validateBaseURL(valid); err != nil {
			t.Fatalf("expected %q to be valid: %v", valid, err)
		}
	}
	for _, invalid := range []string{"hs.example.com", "ftp://hs.example.com", "https://hs.example.com/admin", "https://user:pass@hs.example.com"} {
		if err := validateBaseURL(invalid); err == nil {
			t.Fatalf("expected %q to be rejected", invalid)
		}
	}
}

func TestValidateInstallOptions(t *testing.T) {
	options := testInstallOptions()
	options.binaryPath = "/bin/true"
	if err := validateInstallOptions(options); err != nil {
		t.Fatalf("expected options to be valid: %v", err)
	}

	options.headplaneListen = options.idpListen
	if err := validateInstallOptions(options); err == nil {
		t.Fatal("expected duplicate internal listener to be rejected")
	}
}

func TestValidateManagedPathRejectsSystemRoots(t *testing.T) {
	for _, path := range []string{"/", "/etc", "/usr", "/var", "relative/path", "/var/lib/head scale-console"} {
		if err := validateManagedPath(path); err == nil {
			t.Fatalf("expected %q to be rejected", path)
		}
	}
	for _, path := range []string{"/etc/headscale-console", "/var/lib/headscale-console"} {
		if err := validateManagedPath(path); err != nil {
			t.Fatalf("expected %q to be accepted: %v", path, err)
		}
	}
}
