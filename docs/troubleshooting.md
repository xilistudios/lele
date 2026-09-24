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

Check the gateway service:

```bash
lele status
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
- the session/agent is using the expected provider or explicit `provider:model`

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

## `read_video is not available: the current model supports neither video nor vision`

`read_video` is hidden (and blocked at execution time) unless the session
model's config sets the `video` **or** the `vision` capability flag:

```json
"models": {
  "gemini-2.5-pro": {
    "model": "google/gemini-2.5-pro",
    "video": true
  }
}
```

Set the flag on the model entry under `providers.<name>.models`:

- `"video": true` — native `video_url` delivery (video-only models use this;
  with both flags, `mode=auto` picks native).
- `"vision": true` — models without native video get `read_video` through
  frames mode (keyframes + transcript); no `video` flag needed.

Providers that accept the `video_url` content part: OpenRouter (Gemini,
Qwen-VL, Kimi, Grok routes), xAI Grok, Qwen-VL (DashScope/vLLM/Ollama), and
Moonshot Kimi via plain URLs. OpenAI and Anthropic do **not** accept native
video — leave `video` unset there and rely on `vision` (frames mode).

### `frames mode requires ffmpeg and ffprobe on PATH`

Frames mode (keyframes + transcript, used for vision-only models) shells
out to `ffmpeg`/`ffprobe` at call time — an optional runtime dependency,
not bundled with lele. Install ffmpeg (provides both binaries), e.g.
`sudo apt install ffmpeg` or `brew install ffmpeg`, so both resolve on
`PATH`. Alternative: enable native video via the model's `"video": true`
flag so `mode=auto` delivers a `video_url` instead and never touches
ffmpeg.

### Provider returns 400 on `video_url`

The endpoint rejected the `video_url` content part: that provider/model route
does not accept inline video. Disable `"video": true` for that model — with
`"vision": true` set, `mode=auto` then falls back to frames mode (or the tool
becomes hidden again if `vision` is also unset) — or route the model through a
provider that supports it (see the provider matrix in
`docs/agents-models-providers.md`).

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
