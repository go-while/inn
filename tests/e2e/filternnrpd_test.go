package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFilterNnrpdQuotedText: the checkincludedtext check of the sample filter_nnrpd.pl
// rejects a posted article with too much quoted text and accepts one without quotes. The
// check reads the body from $body, which nnrpd sets for filter_post; with
// $hdr{'__BODY__'}, which only innd sets, it saw no lines and accepted every article.
func TestFilterNnrpdQuotedText(t *testing.T) {
	sample, err := os.ReadFile(filepath.Join(prefix(t), "bin", "filter", "filter_nnrpd.pl"))
	if err != nil {
		t.Fatal(err)
	}
	const off = "checkincludedtext => 0,"
	if !strings.Contains(string(sample), off) {
		t.Fatalf("the installed filter_nnrpd.pl has no %q", off)
	}
	filter := strings.Replace(string(sample), off, "checkincludedtext => 1,", 1)
	s := NewSite(t, SiteOptions{
		InnConf: map[string]string{"pathfilter": "@etcdir@"},
		Etc:     map[string]string{"filter_nnrpd.pl": filter},
	})
	c := s.Reader(t)

	// The sample rejects more than 40 lines (includedcutoff) of which more than 60%
	// (includedratio) start with > or :.
	lines := func(prefix string) []string {
		body := make([]string, 50)
		for i := range body {
			body[i] = prefix + "a line of the article"
		}
		return body
	}

	expect(t, c, 340, "POST")
	send(t, c, wire(newArticle(msgID(t), "e2e.test", "quoted", lines("> ")...)))
	if got := status(t, c, 441, "POST of a quoted article"); !strings.Contains(got, "too much quoted text") {
		t.Errorf("POST of a quoted article: %q, want 441 with \"too much quoted text\"", got)
	}

	expect(t, c, 340, "POST")
	send(t, c, wire(newArticle(msgID(t), "e2e.test", "not quoted", lines("")...)))
	status(t, c, 240, "POST of an article without quotes")
	expect(t, c, 205, "QUIT")
}
