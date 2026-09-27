package resources

import "testing"

func TestNormalizeRemotePath(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
		ok   bool
	}{
		"根":      {in: "/", want: "/", ok: true},
		"归一化":    {in: "/opt/custos-machina//compose/", want: "/opt/custos-machina/compose", ok: true},
		"相对路径拒绝": {in: "opt/x", ok: false},
		"空拒绝":    {in: "", ok: false},
		"上跳允许（SSH 用户权限兜底）": {in: "/opt/../etc", want: "/etc", ok: true},
	}
	for name, c := range cases {
		got, err := normalizeRemotePath(c.in)
		if (err == nil) != c.ok || (c.ok && got != c.want) {
			t.Errorf("%s: in=%q got=%q err=%v want=%q ok=%v", name, c.in, got, err, c.want, c.ok)
		}
	}
}
