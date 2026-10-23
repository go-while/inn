module github.com/InterNetNews/inn/tests/e2e

go 1.26.0

require github.com/go-while/nntp-testkit v0.6.0

// Development: the nntp-testkit checkout next to this tree, until v0.6.0 is tagged.
replace github.com/go-while/nntp-testkit => ../../../nntp-testkit
