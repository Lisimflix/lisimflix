# Third-party software in the Lisimflix image

Lisimflix itself is licensed under the [AGPL-3.0](LICENSE). Its source is this repository.

The container image built from [`Dockerfile`](Dockerfile) also ships this program, unmodified:

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
