# Continuous Integration

The files in this directory are used for continuous integration testing.
`ci/install` installs the prerequisite packages (run as root on a Debian
derivative), and `ci/test` runs the tests.  `ci/pynntp` runs the test suite
of [pynntp](https://github.com/greenbender/pynntp), a Python NNTP client,
against an installed INN that it starts; see the comments at its top.

Tests are run automatically via GitHub Actions workflows using these
scripts and the configuration in the `.github/workflows` directory.
