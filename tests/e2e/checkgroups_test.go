package e2e

// docheckgroups and the rmgroup control message: what happens to the descriptions of the
// newsgroups file, with keepdescriptions in inn.conf (issue #328).

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// descriptions returns the newsgroups file as a map from group name to description.
func descriptions(content string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(content, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			m[f[0]] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), f[0]))
		}
	}
	return m
}

// wantDescriptions fails the test unless the newsgroups file has exactly the descriptions in
// want, plus those of the groups outside the h.* hierarchy, which must stay what they were.
func wantDescriptions(t *testing.T, s *Site, want map[string]string) {
	t.Helper()
	got := descriptions(s.ReadFile(t, "db/newsgroups"))
	for _, g := range []string{"e2e.test", "e2e.other", "e2e.moderated", "control", "control.cancel", "junk"} {
		want[g] = "Group " + g + "."
	}
	for g, d := range want {
		if gd, ok := got[g]; !ok {
			t.Errorf("newsgroups lacks %s, want %q", g, d)
		} else if gd != d {
			t.Errorf("newsgroups: %s is %q, want %q", g, gd, d)
		}
	}
	for g, d := range got {
		if _, ok := want[g]; !ok {
			t.Errorf("newsgroups has %s: %q, which should be gone", g, d)
		}
	}
}

// TestDocheckgroupsKeepDescriptions: "docheckgroups -u" replaces the description of a group
// of the checkgroups and adds the missing one. It removes the descriptions of the groups of
// the hierarchy which are not in the checkgroups, in the active file or not, unless
// keepdescriptions is true in inn.conf; they are then kept, and not reported as obsolete.
func TestDocheckgroupsKeepDescriptions(t *testing.T) {
	const checkgroups = "h.kept\tA group which stays.\n" +
		"h.changed\tThe new description.\n" +
		"h.new\tA new group.\n"
	for _, keep := range []bool{false, true} {
		name := "keepdescriptions=false"
		if keep {
			name = "keepdescriptions=true"
		}
		t.Run(name, func(t *testing.T) {
			o := SiteOptions{
				Groups: append(slices.Clone(DefaultGroups),
					Group{Name: "h.kept", Desc: "A group which stays."},
					Group{Name: "h.changed", Desc: "The old description."},
					Group{Name: "h.old", Desc: "Not in the checkgroups any more."}),
			}
			if keep {
				o.InnConf = map[string]string{"keepdescriptions": "true"}
			}
			s := NewSite(t, o)
			// A description of a group which is not in the active file.
			s.writeFile(t, "db/newsgroups", s.ReadFile(t, "db/newsgroups")+
				"h.gone\tRemoved from the active file long ago.\n")

			out := s.RunInput(t, checkgroups, "docheckgroups", "-u")
			t.Logf("docheckgroups -u:\n%s", out)

			want := map[string]string{
				"h.kept":    "A group which stays.",
				"h.changed": "The new description.",
				"h.new":     "A new group.",
			}
			if keep {
				want["h.old"] = "Not in the checkgroups any more."
				want["h.gone"] = "Removed from the active file long ago."
			}
			wantDescriptions(t, s, want)

			// The report on obsolete descriptions.
			obsolete := ""
			if _, rest, ok := strings.Cut(out, "descriptions are obsolete"); ok {
				obsolete, _, _ = strings.Cut(rest, "# The file")
			}
			if !strings.Contains(obsolete, "h.changed") {
				t.Errorf("the old description of h.changed is not reported as obsolete:\n%s", out)
			}
			for _, g := range []string{"h.old", "h.gone"} {
				if reported := strings.Contains(obsolete, g+"\t"); reported == keep {
					t.Errorf("description of %s reported as obsolete: %v, want %v\n%s",
						g, reported, !keep, out)
				}
			}
			// The group is still reported as one to remove from the active file: only its
			// description is kept.
			if !strings.Contains(out, "ctlinnd rmgroup h.old") {
				t.Errorf("no rmgroup command for h.old:\n%s", out)
			}
		})
	}
}

// TestRmgroupKeepDescriptions: controlchan processes an rmgroup control message by removing
// the group from the active file and its description from the newsgroups file; with
// keepdescriptions true in inn.conf the description is kept.
func TestRmgroupKeepDescriptions(t *testing.T) {
	if err := exec.Command("perl", "-MMIME::Parser", "-e", "1").Run(); err != nil {
		t.Skip("controlchan needs the Perl module MIME::Parser (libmime-tools-perl)")
	}
	for _, keep := range []bool{false, true} {
		name := "keepdescriptions=false"
		if keep {
			name = "keepdescriptions=true"
		}
		t.Run(name, func(t *testing.T) {
			o := SiteOptions{
				Groups: append(slices.Clone(DefaultGroups),
					Group{Name: "h.first", Desc: "The first group."},
					Group{Name: "h.second", Desc: "The second group."}),
				Newsfeeds: "ME:*::\n" +
					"controlchan!\\\n\t:!*,control,control.*,!control.cancel\\\n" +
					"\t:AC,Tc,Wnsm:@bindir@/controlchan\n",
				// The last matching line wins.
				Etc: map[string]string{"control.ctl": "all:*:*:drop\nrmgroup:*:h.*:doit\n"},
			}
			if keep {
				o.InnConf = map[string]string{"keepdescriptions": "true"}
			}
			s := NewSite(t, o)

			// controlchan handles the messages one after the other: when it is done with
			// the second one, it is done with the first one.
			p := s.Peer(t)
			for _, g := range []string{"h.first", "h.second"} {
				id := msgID(t)
				art := newArticle(id, "control", "cmsg rmgroup "+g, "rmgroup by e2e")
				art = slices.Insert(art, slices.Index(art, ""),
					"Control: rmgroup "+g, "Approved: e2e@example.invalid")
				expect(t, p, 335, "IHAVE %s", id)
				send(t, p, wire(art))
				status(t, p, 235, "IHAVE "+id)
			}
			expect(t, p, 205, "QUIT")

			waitFor(t, 30*time.Second, "controlchan to remove h.second from the active file",
				func() bool { return !strings.Contains(s.ReadFile(t, "db/active"), "h.second ") })
			if !keep {
				waitFor(t, 10*time.Second, "controlchan to remove the description of h.second",
					func() bool { return descriptions(s.ReadFile(t, "db/newsgroups"))["h.second"] == "" })
			}
			if strings.Contains(s.ReadFile(t, "db/active"), "h.first ") {
				t.Error("h.first is still in the active file")
			}
			want := map[string]string{}
			if keep {
				want["h.first"] = "The first group."
				want["h.second"] = "The second group."
			}
			wantDescriptions(t, s, want)
		})
	}
}
