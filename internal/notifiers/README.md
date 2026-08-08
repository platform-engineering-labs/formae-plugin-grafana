# Notifier metadata snapshot

`alert-notifiers.json` is a snapshot of `GET /api/alert-notifiers` — Grafana's
legacy alerting notifier metadata: the list of notifier types and, for each,
the settings-form fields it accepts (including nested subforms). `metadata.go`
embeds this file as the fallback vocabulary (`Baked()`) used when a live
Grafana isn't available, and exposes `Parse` for metadata fetched from a
running instance.

## Provenance

The committed snapshot targets Grafana **13.1.3**. It was produced offline by
replicating the `/api/alert-notifiers` handler against
`github.com/grafana/alerting` at the revision Grafana 13.1.3 pins, rather than
by calling a live server.

## Refreshing

Normally, regenerate the snapshot from a running Grafana of the pinned
version:

```sh
scripts/gen/fetch-notifiers.sh <grafana-url>
```

This curls `GET /api/alert-notifiers` and rewrites `alert-notifiers.json` in
place. Regeneration is a deliberate act taken at release time, against a
pinned Grafana version, producing a reviewable diff — it is not run as part
of routine builds or CI.

The Pkl type for a contact point's settings is derived from this snapshot, so
after refreshing it run `make generate` and commit the regenerated
`schema/pkl/alerting/generated/contact_point_settings.pkl` alongside. A unit
test fails when the committed module no longer matches the snapshot.
