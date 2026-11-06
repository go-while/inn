# main-matrix

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
| BSD, illumos | FreeBSD, OpenBSD, NetBSD, DragonFly BSD, OmniOS (VMs) | build, `make check` |
| macOS | only when started by hand with "macos" | build, `make check` |

It runs on a push to this branch and by hand (Actions, main-matrix, "Run
workflow": give one ref such as a tag `inn-2.7.4` instead of main and 2.7, or
untick the patches to test upstream as it is).

## Patches

`patches/<ref>/*.patch` (`/` in a ref becomes `_`) are `git format-patch` files of fixes that upstream needs for
this matrix to build, e.g. a fork PR waiting for review. `apply-patches` skips
a patch that upstream already has (it applies in reverse) and fails on one that
applies neither way. Remove a patch once upstream has it.

| Patch | Refs | Fork PR | Why |
|---|---|---|---|
| `0001-glibc-const.patch` | main, 2.7 | go-while/inn#5 | glibc 2.43 (C23) const string functions break `make warnings` |
| `0002-clang-pre-c11-compat.patch` | main, 2.7 | branch `fix-clang-pre-c11` | glibc 2.43 `_Generic` macros trip Clang's `-Wpre-c11-compat` |

`e2e-distro` is a copy of `ci/e2e-distro` from the fork's `e2e` branch.
