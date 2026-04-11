# Troubleshooting

This page covers common issues when running Lele locally.

## `Error loading config`

Check that the config exists:

```bash
lele status
```

If needed, recreate it:

```bash
lele onboard
```

## Web UI Loads But Cannot Connect

Check both services:

```bash
lele web status
lele gateway
```

Typical causes:

- gateway is not running
- native channel is not enabled
- pairing token expired
- CORS origin not allowed

## Pairing PIN Does Not Work

Typical causes:

- PIN expired
- native channel config changed after generating the PIN
- too many paired clients already exist

Generate a fresh PIN:

```bash
lele client pin --device "Desktop"
```

## Model Not Available In UI Or Session

Check:

- the provider exists in `providers`
- the provider has the expected `models` aliases
- the session/agent is using the expected provider or explicit `provider/model`

See:

- `docs/agents-models-providers.md`
- `docs/model-routing.md`

## Provider Auth Problems

Check current auth state:

```bash
lele auth status
```

Re-authenticate if needed:

```bash
lele auth login --provider openai
lele auth login --provider anthropic
```

## No Channels Enabled

If `lele gateway` starts but warns that no channels are enabled, check the `channels` section in config and verify the required credentials are present.

## File Upload Fails

Typical causes:

- file exceeds `max_upload_size_mb`
- invalid multipart request
- upload directory could not be created

## Native Session Access Denied

The native API enforces session ownership.

Make sure you only use session keys in your own namespace, for example:

- `native:<client_id>`
- `native:<client_id>:<suffix>`

## Web Server Not Starting

Check:

- another process already uses the port
- web assets were not built

Rebuild if needed:

```bash
make build
```

Inspect the web log:

```text
~/.lele/logs/web.log
```

## Gateway Starts But Channel Does Not Respond

Typical causes:

- missing token or URL in channel config
- external webhook/bridge not reachable
- provider credentials missing

## Logs To Inspect

- `~/.lele/logs/info-YYYY-MM-DD.log`
- `~/.lele/logs/errors-YYYY-MM-DD.log`
- `~/.lele/logs/web.log`

## Related Docs

- `docs/installation-and-onboarding.md`
- `docs/channel-setup.md`
- `docs/client-api.md`
