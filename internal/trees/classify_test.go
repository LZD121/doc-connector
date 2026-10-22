package trees

import "testing"

func e(oid string) Entry { return Entry{Mode: 0o100644, OID: oid} }

func TestClassifyAllRows(t *testing.T) {
	const pid = "aaaaaaaaaaaa"
	cases := []struct {
		name    string
		b, l, r Tree
		want    Class
		status  string
	}{
		{"local-add", Tree{}, Tree{"x": e(pid)}, Tree{}, LocalAdd, "A"},
		{"remote-add", Tree{}, Tree{}, Tree{"x": e(pid)}, RemoteAdd, "A"},
		{"add/add", Tree{}, Tree{"x": e("111111111111")}, Tree{"x": e("222222222222")}, AddAdd, "C"},
		{"converged", Tree{}, Tree{"x": e(pid)}, Tree{"x": e(pid)}, Converged, "="},
		{"unchanged", Tree{"x": e(pid)}, Tree{"x": e(pid)}, Tree{"x": e(pid)}, Unchanged, ""},
		{"local-mod", Tree{"x": e("111111111111")}, Tree{"x": e("222222222222")}, Tree{"x": e("111111111111")}, LocalMod, "M"},
		{"remote-mod", Tree{"x": e("111111111111")}, Tree{"x": e("111111111111")}, Tree{"x": e("222222222222")}, RemoteMod, "M"},
		{"both-modified", Tree{"x": e("111111111111")}, Tree{"x": e("222222222222")}, Tree{"x": e("333333333333")}, BothModified, "C"},
		{"both-same", Tree{"x": e("111111111111")}, Tree{"x": e("222222222222")}, Tree{"x": e("222222222222")}, BothSame, "="},
		{"local-delete", Tree{"x": e(pid)}, Tree{}, Tree{"x": e(pid)}, LocalDelete, "D"},
		{"delete/modify", Tree{"x": e("111111111111")}, Tree{}, Tree{"x": e("222222222222")}, DeleteModify, "C"},
		{"remote-delete", Tree{"x": e(pid)}, Tree{"x": e(pid)}, Tree{}, RemoteDelete, "D"},
		{"modify/delete", Tree{"x": e("111111111111")}, Tree{"x": e("222222222222")}, Tree{}, ModifyDelete, "C"},
		{"both-deleted", Tree{"x": e(pid)}, Tree{}, Tree{}, BothDeleted, ""},
	}
	for _, c := range cases {
		actions, _ := Classify(c.b, c.l, c.r)
		if len(actions) != 1 {
			t.Fatalf("%s: got %d actions", c.name, len(actions))
		}
		if actions[0].Class != c.want || actions[0].Status != c.status {
			t.Errorf("%s: class=%v status=%q, want %v %q", c.name, actions[0].Class, actions[0].Status, c.want, c.status)
		}
	}
}

func TestClassifyModeChange(t *testing.T) {
	b := Tree{"x": {Mode: 0o100644, OID: "aaaa"}}
	l := Tree{"x": {Mode: 0o100755, OID: "aaaa"}}
	r := Tree{"x": {Mode: 0o100644, OID: "aaaa"}}
	actions, summary := Classify(b, l, r)
	if actions[0].Class != LocalMod || summary.String() != "ahead" {
		t.Errorf("chmod not treated as a local modification: %v %s", actions[0].Class, summary.String())
	}
}

func TestSummaryDirection(t *testing.T) {
	base := Tree{"a": e("1"), "b": e("1")}
	local := Tree{"a": e("2"), "b": e("1")}
	remote := Tree{"a": e("1"), "b": e("2")}
	if _, s := Classify(base, local, Tree{"a": e("1"), "b": e("1")}); s.String() != "ahead" {
		t.Errorf("ahead summary = %s", s.String())
	}
	if _, s := Classify(base, Tree{"a": e("1"), "b": e("1")}, remote); s.String() != "behind" {
		t.Errorf("behind summary = %s", s.String())
	}
	_, s := Classify(base, local, remote)
	if s.String() != "diverged" || s.Conflicts != 0 {
		t.Errorf("diverged summary = %+v", s)
	}
	if _, s := Classify(Tree{}, Tree{}, Tree{}); s.String() != "clean" {
		t.Errorf("empty summary = %s", s.String())
	}
}

func TestClobber(t *testing.T) {
	base := Tree{"same": e("1"), "local": e("1"), "remote": e("1"), "gone": e("1")}
	local := Tree{"same": e("1"), "local": e("2"), "remote": e("1"), "gone": e("1")}
	remote := Tree{"same": e("1"), "local": e("1"), "remote": e("2"), "gone": e("1")}
	// remote-deleted path also clobbers when local is unchanged.
	got := Clobber(base, local, remote, nil)
	want := []string{"remote"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("clobber = %v, want %v", got, want)
	}
	if got := Clobber(base, local, remote, map[string]bool{"remote": true}); len(got) != 0 {
		t.Errorf("resolved path still clobbers: %v", got)
	}

	// A remote deletion of an unchanged local file is a clobber too.
	delBase := Tree{"gone": e("1")}
	delLocal := Tree{"gone": e("1")}
	if got := Clobber(delBase, delLocal, Tree{}, nil); len(got) != 1 {
		t.Errorf("remote-delete not clobbered: %v", got)
	}
}
