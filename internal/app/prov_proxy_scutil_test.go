package app

import "testing"

func TestParseScutilProxyOutput(t *testing.T) {
	status := parseScutilProxyOutput(`<dictionary> {
  ExceptionsList : <array> {
    0 : localhost
    1 : 127.0.0.1
    2 : *.local
  }
  HTTPEnable : 1
  HTTPPort : 7890
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7891
  HTTPSProxy : proxy.example.test
  ProxyAutoConfigEnable : 0
  ProxyAutoDiscoveryEnable : 0
}`)

	if !status.Enabled {
		t.Fatalf("expected proxy to be enabled: %#v", status)
	}
	if status.HTTPProxy != "http://127.0.0.1:7890" {
		t.Fatalf("unexpected HTTP proxy: %q", status.HTTPProxy)
	}
	if status.HTTPSProxy != "http://proxy.example.test:7891" {
		t.Fatalf("unexpected HTTPS proxy: %q", status.HTTPSProxy)
	}
	if status.NoProxy != "localhost,127.0.0.1,*.local" {
		t.Fatalf("unexpected no-proxy list: %q", status.NoProxy)
	}
	if status.PACURL != "" || status.PACUnsupported {
		t.Fatalf("unexpected PAC state: %#v", status)
	}
}

func TestParseScutilProxyOutputUsesSocksProxyWhenNoHTTPProxyExists(t *testing.T) {
	status := parseScutilProxyOutput(`<dictionary> {
  SOCKSEnable : 1
  SOCKSPort : 1080
  SOCKSProxy : 127.0.0.1
}`)

	if !status.Enabled {
		t.Fatalf("expected SOCKS proxy to be enabled: %#v", status)
	}
	for name, value := range map[string]string{
		"HTTP":  status.HTTPProxy,
		"HTTPS": status.HTTPSProxy,
	} {
		if value != "socks5://127.0.0.1:1080" {
			t.Errorf("unexpected %s proxy: %q", name, value)
		}
	}
}

func TestParseScutilProxyOutputReportsPACWithoutEnablingFixedProxy(t *testing.T) {
	status := parseScutilProxyOutput(`<dictionary> {
  ProxyAutoConfigEnable : 1
  ProxyAutoConfigURLString : http://proxy.example.test/proxy.pac
  ProxyAutoDiscoveryEnable : 0
}`)

	if status.Enabled {
		t.Fatalf("PAC-only configuration must not be treated as a fixed proxy: %#v", status)
	}
	if status.PACURL != "http://proxy.example.test/proxy.pac" {
		t.Fatalf("unexpected PAC URL: %q", status.PACURL)
	}
	if !status.PACUnsupported {
		t.Fatal("expected PAC configuration to be marked unsupported")
	}
}

func TestParseScutilProxyOutputIgnoresInvalidFixedProxy(t *testing.T) {
	status := parseScutilProxyOutput(`<dictionary> {
  HTTPEnable : 1
  HTTPPort : 70000
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : not-a-port
  HTTPSProxy : proxy.example.test
}`)

	if status.Enabled {
		t.Fatalf("invalid proxy ports must not enable the proxy: %#v", status)
	}
	if status.HTTPProxy != "" || status.HTTPSProxy != "" {
		t.Fatalf("invalid proxy values should be discarded: %#v", status)
	}
}
