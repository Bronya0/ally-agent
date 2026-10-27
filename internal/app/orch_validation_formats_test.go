// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAutoValidationDataFormats(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{"config.yaml", "name: Ally\nitems:\n  - one\n  - two\n", true},
		{"multi.yml", "---\na: 1\n---\nb: 2\n", true},
		{"invalid.yaml", "key: [unterminated\n", false},
		{"later.yml", "---\na: 1\n---\nb: [broken\n", false},
		{"tags.yaml", "value: !CustomTag x\nbase: &base {x: 1}\ncopy: *base\n", true},
		{"unicode.YAML", "\ufeff名字: 测试\n", true},
		{"valid.xml", "<?xml version=\"1.0\"?><root><item a=\"1\"/></root>", true},
		{"mismatch.xml", "<root><item></root>", false},
		{"multiple.xml", "<one/><two/>", false},
		{"empty.xml", "", false},
		{"outside.xml", "text<root/>", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := validationSandbox(t)
			writeToolTestFile(t, root, tc.name, tc.source)
			a := &App{}
			cfg := ConfigState{Workspace: root}
			if got := a.validateChangedFiles(context.Background(), cfg, []string{tc.name}); got != "" {
				t.Fatalf("default must be off: %s", got)
			}
			enabled := true
			cfg.AutoValidationYAML, cfg.AutoValidationXML = &enabled, &enabled
			got := a.validateChangedFiles(context.Background(), cfg, []string{tc.name})
			want := "自动校验通过"
			if !tc.valid {
				want = "自动校验失败"
			}
			if !strings.Contains(got, want) {
				t.Fatalf("want %s, got %s", want, got)
			}
			if !tc.valid && !strings.Contains(got, tc.name) {
				t.Fatalf("missing file diagnostic: %s", got)
			}
			disabled := false
			cfg.AutoValidationYAML, cfg.AutoValidationXML = &disabled, &disabled
			if got := a.validateChangedFiles(context.Background(), cfg, []string{tc.name}); got != "" {
				t.Fatalf("disabled: %s", got)
			}
		})
	}
}

func TestDataValidationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, parse := range []func(context.Context, []byte) error{validateYAMLSyntax, validateXMLSyntax} {
		if err := parse(ctx, []byte("{}")); err != context.Canceled {
			t.Fatalf("expected cancellation: %v", err)
		}
	}
}

func TestDataValidationConfigRoundTrip(t *testing.T) {
	for _, field := range []func(*ConfigState) **bool{
		func(c *ConfigState) **bool { return &c.AutoValidationYAML },
		func(c *ConfigState) **bool { return &c.AutoValidationXML },
	} {
		enabled, disabled := true, false
		base := ConfigState{}
		*field(&base) = &enabled
		for _, next := range []ConfigState{cloneConfig(base), settingsConfig(base, ConfigState{}), mergeConfig(ConfigState{}, base)} {
			if !validationEnabled(*field(&next)) {
				t.Fatal("enabled setting lost")
			}
		}
		cloned := cloneConfig(base)
		**field(&cloned) = false
		if !enabled {
			t.Fatal("clone aliases original flag")
		}
		req := ConfigState{}
		*field(&req) = &disabled
		for _, next := range []ConfigState{settingsConfig(base, req), mergeConfig(base, req)} {
			if validationEnabled(*field(&next)) {
				t.Fatal("explicit disable ignored")
			}
		}
		data, err := json.Marshal(base)
		if err != nil {
			t.Fatal(err)
		}
		var loaded ConfigState
		if err := json.Unmarshal(data, &loaded); err != nil {
			t.Fatal(err)
		}
		if !validationEnabled(*field(&loaded)) {
			t.Fatal("JSON round trip lost setting")
		}
	}
}

func TestDataValidationSkipsUnsupportedFormats(t *testing.T) {
	root := validationSandbox(t)
	writeToolTestFile(t, root, "chart/Chart.yaml", "name: example")
	writeToolTestFile(t, root, "chart/templates/deploy.yaml", "value: {{ .Values.name }}")
	writeToolTestFile(t, root, "templates/broken.yaml", "value: [broken")
	writeToolTestFile(t, root, "custom.xml", "<!DOCTYPE r [<!ENTITY x SYSTEM 'file:///missing'>]><r>&x;</r>")
	writeToolTestFile(t, root, "unknown.xml", "<?xml version=\"1.0\" encoding=\"unsupported-charset\"?><root/>")
	writeToolTestFile(t, root, "utf16.xml", string([]byte{0xff, 0xfe, '<', 0, 'r', 0, '/', 0, '>', 0}))
	enabled := true
	a := &App{}
	cfg := ConfigState{Workspace: root, AutoValidationYAML: &enabled, AutoValidationXML: &enabled}
	for _, path := range []string{"chart/templates/deploy.yaml", "custom.xml", "unknown.xml", "utf16.xml"} {
		if got := a.validateChangedFiles(context.Background(), cfg, []string{path}); !strings.Contains(got, "自动校验跳过") {
			t.Fatalf("%s should be skipped: %s", path, got)
		}
	}
	if got := a.validateChangedFiles(context.Background(), cfg, []string{"templates/broken.yaml"}); !strings.Contains(got, "自动校验失败") {
		t.Fatalf("non-Helm template must be checked: %s", got)
	}
	if got := a.validateChangedFiles(context.Background(), cfg, []string{"chart/templates/deploy.yaml", "templates/broken.yaml"}); !strings.Contains(got, "自动校验失败") {
		t.Fatalf("skip hid a real error: %s", got)
	}
}

func TestXMLDeclaredEncoding(t *testing.T) {
	data := append([]byte("<?xml version=\"1.0\" encoding=\"windows-1252\"?><root>caf"), 0xe9)
	data = append(data, []byte("</root>")...)
	if err := validateXMLSyntax(context.Background(), data); err != nil {
		t.Fatalf("valid XML encoding rejected: %v", err)
	}
}

func TestJSONCWellKnownVSCodeFiles(t *testing.T) {
	root := validationSandbox(t)
	enabled := true
	a := &App{}
	cfg := ConfigState{Workspace: root, AutoValidationJSON: &enabled}
	for _, path := range []string{".vscode/settings.json", ".vscode/launch.json", ".vscode/tasks.json", ".vscode/extensions.json"} {
		writeToolTestFile(t, root, path, `{ // comment
 "x": 1,
}`)
		if got := a.validateChangedFiles(context.Background(), cfg, []string{path}); !strings.Contains(got, "自动校验跳过") {
			t.Fatalf("JSONC %s: %s", path, got)
		}
	}
	writeToolTestFile(t, root, "other/settings.json", `{ // comment
}`)
	if got := a.validateChangedFiles(context.Background(), cfg, []string{"other/settings.json"}); !strings.Contains(got, "自动校验失败") {
		t.Fatalf("ordinary JSON ignored: %s", got)
	}
}
