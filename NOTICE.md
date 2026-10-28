# Third-party software in the Lisimflix image

Lisimflix itself is licensed under the [AGPL-3.0](LICENSE). Its source is this repository.

The container image built from [`Dockerfile`](Dockerfile) also ships third-party software:
the Go modules and web packages compiled into Lisimflix, jellyfin-ffmpeg, and the Debian
packages of the base image.

## Go modules and web packages

Lisimflix's binary includes the Go standard library, the Go modules it imports and the web
packages bundled into its web app (React, plus small runtime helpers from Vite and Rolldown).
Their licences (BSD-3-Clause, MIT and others) require their notices with the binary. The image
build collects every licence text into `/usr/share/doc/lisimflix/THIRD_PARTY_LICENSES`, so the
list always matches what was built, and the build fails if a module or package has no licence
file.

## Debian packages of the base image

The runtime base is `gcr.io/distroless/base-debian12` (pinned by digest in `Dockerfile`). It
carries a few Debian 12 packages, including glibc (LGPL-2.1) and OpenSSL (Apache-2.0). Their
copyright files are in the image at `/usr/share/doc/<package>/copyright`, and the exact
package versions at `/var/lib/dpkg/status.d/<package>`. The source of each version is
on [snapshot.debian.org](https://snapshot.debian.org/), under the package's `Source` name
(for example glibc `2.36-9+deb12u14` at <https://snapshot.debian.org/package/glibc/2.36-9%2Bdeb12u14/>).
How the base image is built: <https://github.com/GoogleContainerTools/distroless>.

## jellyfin-ffmpeg (ffprobe only)

| | |
|---|---|
| Version | `v8.1.3-1`, the `portable_linux64-gpl` build |
| In the image | `/usr/lib/jellyfin-ffmpeg/ffprobe` |
| Licence | GNU GPL v3. The texts are in the image at `/usr/share/doc/jellyfin-ffmpeg/` (`COPYING.GPLv3`, `LICENSE.md`) |
| How Lisimflix uses it | As a separate program, run without a shell to read media metadata. Lisimflix doesn't link to it |
| Changes | None. The binary is the upstream release asset, checked by its sha256 (see `Dockerfile`) |
| Corresponding source | The upstream repository at tag [`v8.1.3-1`](https://github.com/jellyfin/jellyfin-ffmpeg/tree/v8.1.3-1), which includes its build scripts. Archive: <https://github.com/jellyfin/jellyfin-ffmpeg/archive/refs/tags/v8.1.3-1.tar.gz> |

If that source ever becomes unavailable upstream, ask the Lisimflix maintainers in an issue
and we'll provide it, as GPLv3 §6 requires for anyone who received the image from us.

When the `Dockerfile` moves to another jellyfin-ffmpeg release, update the version and both
links above in the same change.
