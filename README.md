# main-matrix

> **This is a test fork of INN, not meant for users.** For INN itself, its releases and its
> issue tracker, go to [InterNetNews/inn](https://github.com/InterNetNews/inn). Developers and
> their agents work here: fix branches become pull requests upstream, and the `integration`
> branch merges all of them so that they can be tested together first.

CI only: this branch holds no INN sources. Its workflow
(`.github/workflows/main-matrix.yaml`) checks out upstream InterNetNews/inn
branches `main` (the coming 2.8.0) and `2.7` (the stable line, the coming
2.7.5), applies the patches in `patches/<ref>/` that upstream does not have
yet, and builds and tests each on:

| Jobs | Systems | What runs |
|---|---|---|
| Ubuntu | latest (26.04) gcc and clang, 24.04 gcc, 24.04 ARM64 gcc | upstream `ci/install` + `ci/test` (`make warnings`, three configurations) |
| Sanitizers | Ubuntu latest, ASan + UBSan | build, `make check` |
| Linux distributions | 20 container images (`e2e-distro`) | build, `make check` |
| e2e tests of the fork (informational) | Ubuntu 26.04 container | the Go e2e tests from the fork's `e2e` branch against plain upstream: the failures list which fork fixes upstream lacks; never fails the run |
| BSD, illumos | FreeBSD, OpenBSD, NetBSD, DragonFly BSD, OmniOS (VMs) | build, `make check` |
| macOS | only when started by hand with "macos" | build, `make check` |

It runs on a push to this branch and by hand (Actions, main-matrix, "Run
workflow": give one ref such as a tag `inn-2.7.4` instead of main and 2.7, or
untick the patches to test upstream as it is).  The `repo` input fetches the
ref from another repository, e.g. `repo=go-while/inn ref=integration` tests
the fork's integration branch (upstream main with all open fork PRs merged);
pushes and the weekly run always test InterNetNews/inn.

## Patches

`patches/<ref>/*.patch` (`/` in a ref becomes `_`) are `git format-patch` files of fixes that upstream needs for
this matrix to build, e.g. a fork PR waiting for review. `apply-patches` skips
a patch that upstream already has (it applies in reverse) and fails on one that
applies neither way. Remove a patch once upstream has it.

| Patch | Refs | Fork PR | Why |
|---|---|---|---|
| `0001-glibc-const.patch` | main, 2.7 | go-while/inn#5 | glibc 2.43 (C23) const string functions break `make warnings` |
| `0002-clang-pre-c11-compat.patch` | main, 2.7 | branch `fix-clang-pre-c11` | glibc 2.43 `_Generic` macros trip Clang's `-Wpre-c11-compat` |
| `0003`-`0007` `tests-*` | main, 2.7 | branch `fix-bsd-tests` | `make check` on the BSDs: configured Perl for the auth helper, bcrypt fixture without DES, close Xref searches, stop innconf tests after a read failure |

`e2e-distro` is a copy of `ci/e2e-distro` from the fork's `e2e` branch.
