package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// bodyPerlFilter is a Perl filter_art that writes $hdr{__BODY__} to etc/body.out and
// accepts every article.
const bodyPerlFilter = `sub filter_art {
    my $body = $hdr{'__BODY__'};
    return "no __BODY__" unless defined $body;
    open(my $fh, '>', '@etcdir@/body.out') or return "cannot write body.out: $!";
    binmode $fh;
    print $fh $body;
    close $fh;
    return "";
}
1;
`

// bodyPythonFilter does the same as bodyPerlFilter with art['__BODY__'] (Python 3).
const bodyPythonFilter = `from INN import set_filter_hook


class Filter:
    def filter_art(self, art):
        body = art.get("__BODY__")
        if body is None:
            return "no __BODY__"
        with open("@etcdir@/body.out", "wb") as f:
            f.write(bytes(body))
        return ""


set_filter_hook(Filter())
`

// TestFilterBodyPerl: the Perl filter_art gets in $hdr{__BODY__} the body of the article as
// it arrived (CRLF line ends, dot-stuffing kept), without the "." line that ends the article.
func TestFilterBodyPerl(t *testing.T) {
	testFilterBody(t, "filter_innd.pl", bodyPerlFilter)
}

// TestFilterBodyPython: the same with art['__BODY__'] in the Python filter_art, which got the
// body with a final ".\r". Skipped when innd was built without Python (configure --with-python).
func TestFilterBodyPython(t *testing.T) {
	innd, err := os.ReadFile(filepath.Join(prefix(t), "bin", "innd"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(innd, []byte("python interpreter initialized OK")) {
		t.Skip("innd was built without Python")
	}
	testFilterBody(t, "filter_innd.py", bodyPythonFilter)
}

func testFilterBody(t *testing.T, file, filter string) {
	s := NewSite(t, SiteOptions{
		InnConf: map[string]string{"pathfilter": "@etcdir@"},
		Etc:     map[string]string{file: filter},
	})
	bodyOut := filepath.Join(s.Dir, "etc", "body.out")
	p := s.Peer(t)
	for _, tc := range []struct {
		name string
		body []string
		want string
	}{
		{"lines", []string{"first line", ".dotted", "", "last line"},
			"first line\r\n..dotted\r\n\r\nlast line\r\n"},
		{"one empty line", []string{""}, "\r\n"},
		{"no body", nil, ""},
	} {
		if err := os.Remove(bodyOut); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		id := msgID(t)
		expect(t, p, 335, "IHAVE %s", id)
		send(t, p, wire(newArticle(id, "e2e.test", "body "+tc.name, tc.body...)))
		status(t, p, 235, "IHAVE "+id)
		got, err := os.ReadFile(bodyOut)
		if err != nil {
			t.Fatalf("%s: the filter wrote no __BODY__: %v", tc.name, err)
		}
		if string(got) != tc.want {
			t.Errorf("%s: __BODY__ = %q (%d bytes), want %q", tc.name, got, len(got), tc.want)
		}
	}
	expect(t, p, 205, "QUIT")
}
