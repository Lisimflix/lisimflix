Webhook payloads shaped as Sonarr v4 and Radarr v6 send them, written from
their `develop` source (`src/NzbDrone.Core/Notifications/Webhook/`, October
2026): camelCase properties, PascalCase `eventType`. Values are made up; only
the fields LIB-4 reads matter. `sonarr_test.json` mirrors Sonarr's own
connection-test payload (`BuildTestPayload`).
