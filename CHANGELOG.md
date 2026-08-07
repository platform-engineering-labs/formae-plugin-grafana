# Changelog

All notable changes to the formae Grafana plugin are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Install with `sudo formae plugin install grafana` on the host that runs the
formae agent.

## [Unreleased]

### Added

- The Grafana target `Config` takes an `auth` block that selects an
  authentication strategy: `TokenAuth` for a service account token or API key,
  `BasicAuth` for a username and password. Every credential field accepts a
  literal string or a resolvable, so a service account token can now be sourced
  from a formae-managed secret and resolved live at apply time. Previously only
  basic auth could be sourced that way, and a token could reach the plugin only
  through the `GRAFANA_AUTH` environment variable, which forced a target wanting
  secret-sourced credentials onto instance-superuser admin basic auth. When
  `auth` is set, `GRAFANA_AUTH` is not consulted and an incomplete block is an
  error rather than a silent fallback to the environment. Omitting `auth` keeps
  the existing `GRAFANA_AUTH` behaviour unchanged.

- `NotificationPolicy.receiver` now accepts a resolvable to a contact point's
  name (`myContactPoint.res.name`) as well as a literal string, and
  `ContactPoint` exposes a `res` resolvable to support it. Grafana rejects a
  policy tree naming a receiver that does not exist, so previously a first apply
  could send the policy before its contact point existed and fail, and a destroy
  could delete the contact point before the policy was reset, leaving the tree
  inconsistent. Using the resolvable makes the dependency explicit, so formae
  creates the contact point first and resets the policy first on teardown.
  Receivers named inside `routes` are part of an opaque JSON string and still
  carry no dependency edge.

### Changed

- The target `Config` fields `username` and `password` are replaced by the
  `auth` block: write `auth = new grafana.BasicAuth { username = …; password = … }`.
  The fields existed only in the `0.1.7-dev.0` prerelease, so no released
  version is affected.

### Fixed

- Updating a contact point no longer records its secret settings in cleartext.
  Grafana stores the secret fields of a contact point encrypted (a Slack `url`,
  a PagerDuty `integrationKey`, a webhook `password`) and returns them as
  `[REDACTED]` on every read path. Update echoed back the settings it had just
  sent instead of the server's view, so the plaintext secret was written into
  recorded state on every update, and stayed permanently diverged from what a
  read reported. Update now reports the state read back from Grafana, which is
  what create already did.

## [0.1.6]

### Fixed

- A Grafana target whose endpoint is unreachable (the backing service is gone —
  for example a torn-down Compose stack or a decommissioned host) is now
  reported as unreachable instead of an opaque internal failure. Transport
  failures on a read — connection refused, DNS failure, dial/read timeout — now
  map to `NetworkFailure`/`ServiceTimeout` rather than the `InternalFailure`
  default, so the agent can tell "unreachable" apart from "deleted" and
  eventually reap a permanently-gone target.

## [0.1.5]

### Changed

- Requires formae 0.87.0 or later.

### Fixed

- Dashboards no longer show formatting-only drift: a dashboard loaded from a
  pretty-printed JSON file (for example `read("dashboard.json")`) is stored by
  Grafana in its own compacted, key-reordered form. Previously every sync
  reported the dashboard as changed and every apply generated a no-op update,
  pure whitespace and key-ordering noise, even when nothing about the dashboard
  had actually changed. The dashboard JSON is now compared by content, so an
  unchanged dashboard stays quiet, and reformatting your source file
  (reindenting or reordering keys) on its own no longer triggers an update.

## [0.1.4]

### Added

- Contact point settings as resolvables (`settingsMap`): `GRAFANA::Alerting::ContactPoint`
  gains a `settingsMap` field whose values accept formae resolvables, so a
  setting can flow in from another resource (even one managed by a different
  plugin) in a single apply. This is what lets a PagerDuty integration key wire
  straight into a Grafana contact point:

    ```pkl
    settingsMap = new Mapping {
        ["integrationKey"] = pdIntegration.res.integrationKey
    }
    ```

    Provide exactly one of `settings` (the JSON-string form) or `settingsMap`.

### Changed

- Provider-immutable fields marked `createOnly`: fields the Grafana API will not
  change in place (for example a contact point's `name`) are now annotated
  `createOnly`, so formae replaces the resource instead of attempting an invalid
  in-place update.
- Requires formae 0.86.0 or later.

## [0.1.3]

### Changed

- Resource type prefix renamed to `GRAFANA::`: the namespace prefix is now
  uppercase to match the formae convention (`namespace = "GRAFANA"`). All ten
  resource types are affected (folder, dashboard, alert_rule, contact_point,
  datasource, message_template, mute_timing, notification_policy,
  service_account, team). The schema generates these names automatically, so PKL
  forma files that import the Grafana schema don't need any change. Forma files
  that reference the resource types by string (e.g. in queries) should switch
  from `Grafana::…` to `GRAFANA::…`.

## [0.1.2]

### Added

- Resolvable target URL: The Grafana target URL now accepts resolvable
  references, so you can wire it directly to another resource's output. For
  example, connect Grafana to a compose stack endpoint without workarounds:

    ```pkl
    config = new grafana.Config {
        url = lgtmStack.res.endpoints.at("lgtm:3000")
    }
    ```

    The `Endpoints`/`EndpointKey` pattern still works but is deprecated and will
    be removed in a future release.

## [0.1.1]

### Fixed

- DataSource resources with default `jsonData` values no longer cause drift on
  every sync. Grafana-populated defaults are now recognised as provider
  defaults.
- Deleting a NotificationPolicy outside of formae (e.g. via the Grafana UI) is
  now correctly detected during sync. Previously the resource remained in
  inventory after an out-of-band delete.
- Dashboards and AlertRules that reference a folder via `folderUid` can now use a
  resolvable reference (`folder.res.uid`), ensuring the folder is created before
  the resources that depend on it.

## [0.1.0]

### Added

- Initial release of the Grafana plugin as a standalone package built on the
  formae Plugin SDK.
