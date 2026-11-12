package command

import (
	"reflect"
	"testing"
)

func TestLiteralWriteTargetsWithCD(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{"cd /srv/app && echo x > output.txt", []string{"/srv/app/output.txt"}},
		{"cd '/srv/my app' && tee config.txt", []string{"/srv/my app/config.txt"}},
		{"cd /srv/app && cd ../other && cp /tmp/source output.txt", []string{"/srv/other/output.txt"}},
		{"cd /srv/app; echo x > first; cd /etc; echo x > second", []string{"/srv/app/first", "/etc/second"}},
		{"(cd /srv/app && echo x > inner); echo x > outer", []string{"/srv/app/inner", "outer"}},
		{"(cd /srv/app && echo x > inner) > outer", []string{"outer", "/srv/app/inner"}},
		{"{ cd /srv/app && echo x > inner; }; echo x > next", []string{"/srv/app/inner", "/srv/app/next"}},
		{"cd /srv/app && echo x > /etc/nginx/nginx.conf", []string{"/etc/nginx/nginx.conf"}},
		{"cd ~/project && echo x > output.txt", []string{"~/project/output.txt"}},
		{"cd /srv/app && echo x > $OUT", nil},
	} {
		var got []string
		for _, target := range LiteralWriteTargetsWithCD(tc.command) {
			got = append(got, target.Path)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("command %q: got %v, want %v", tc.command, got, tc.want)
		}
	}
}
