# Reference installations

These are test-environment records, not installation requirements or release-wide
capacity promises. Use generic paths and configuration placeholders in product docs.
Record test results per app/image, runtime, driver, fixture and client revision.
Private media and credentials are never required in the public test fixture set.

## Lammy's Unraid server — zraid

Pete confirmed ownership on 2026-10-25. The original spec described this installation
as the main target; Lisimflix is now specified for independent self-hosted deployments.
The following details are carried from the original design and remain reported,
not independently verified in the spec review:

- Media: `/mnt/user/dataz/media`, managed by Sonarr/Radarr, with anime separated by tags.
- Reported accelerator: NVIDIA GTX 1070 (Pascal), driver 580 and NVIDIA container
  runtime shared with the existing Plex deployment. The original codec-capability
  statements need the actual pinned-build capability probe and test results.
- Reported compute: 12 CPU threads and 27 GB RAM. These are neither minimum requirements
  nor a recommended capacity specification for other installations.
- Original Tautulli summary: 7 users, peak 3 streams / 2 transcodes, 283 plays
  (218 direct, 62 transcode; 3 unclassified). Platform counts: Android 199, Windows 20,
  Opera 11, iOS 11, PlayStation 8, VIDAA 7. Retain the raw report before interpreting
  the gap or generalising device behaviour.
- Reported motivation: lag/unplayable formats in an earlier Jellyfin attempt. Keep
  reproducible owner-local problem cases, plus shareable equivalent fixtures where
  possible; other contributors must be able to run release checks without this server.

## Additional independent installation — pending selection

Release qualification requires at least one installation administered independently
of Lammy's environment, using different host paths and its own accounts/configuration.
Include a generic non-Unraid Linux-container deployment and a CPU-only test profile;
they may be the same additional installation. Do not assume Pete's production media
server is volunteered or has been tested.

Before testing, record:

1. Installation ID, operator, host OS/architecture, container runtime and image digest.
2. CPU/RAM, optional accelerator/driver, local config/cache capacity and media topology.
3. Fixture/catalogue scale, client models/versions and measured network conditions.
4. Configured stream/job limits, tested conversion paths and measured start/seek/load results.
5. Fresh-setup/backup/restore outcome, limitations and the operator's trial decision.

Use this same record format for every further environment. No test installation's
credentials, hostname or private media path becomes a product default.
