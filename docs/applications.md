# Application Setup

This guide collects basic Mirage setup flows for specific applications. Start
with the application section that matches the tool you want to run inside a
Mirage rootfs.

## OpenClaw

OpenClaw can run against several different OpenClaw-oriented rootfs levels. The
right template depends on how much tooling you want available inside the guest.

### Choose an OpenClaw rootfs level

| Template | Use it when you want |
| --- | --- |
| `openclaw-chat-only` | Node.js, TLS material, and locale/timezone data for the smallest OpenClaw-oriented rootfs |
| `openclaw-work` | `openclaw-chat-only` plus common shell, archive, patching, JSON, and search tools |
| `openclaw-developer` | `openclaw-work` plus Git, editors, Python, SQLite, and common build-toolchain entrypoints |
| `openclaw-admin` | `openclaw-developer` plus networking, process, and capability utilities |
| `openclaw-root` | `openclaw-admin` plus package-management, tracing, debugging, namespace, and filesystem tools |

The leveled `openclaw-*` templates compose from the previous level, so each
later level includes the earlier one plus additional tools and runtime data.

### Install and launch OpenClaw

Pick the template level you want, then generate a dedicated rootfs:

```bash
./bin/mirage rootfs init \
  --template openclaw-developer \
  --output /srv/mirage/openclaw-rootfs
```

If you want a different tool surface, replace `openclaw-developer` with one of
the other OpenClaw templates listed above.

The built-in `openclaw-openai` preset is a good starting point for OpenClaw
installation and launch flows because it recommends the `openclaw-developer`
rootfs level, defaults the working directory to `/workspace`, allows outbound
HTTPS, and also permits the local gateway port `127.0.0.1:18789`.

Install the package inside the generated rootfs:

```bash
./bin/mirage run \
  --rootfs /srv/mirage/openclaw-rootfs \
  --preset openclaw-openai \
  --cwd /workspace \
  -- npm i -g openclaw
```

Run the onboarding flow:

```bash
./bin/mirage run \
  --rootfs /srv/mirage/openclaw-rootfs \
  --preset openclaw-openai \
  --cwd /workspace \
  -- openclaw onboard
```

Start the local OpenClaw gateway on port `18789`:

```bash
./bin/mirage run \
  --rootfs /srv/mirage/openclaw-rootfs \
  --preset openclaw-openai \
  --cwd /workspace \
  -- openclaw gateway --port 18789
```

### Notes

- If you want a smaller or stricter installation environment, choose a narrower
  rootfs level such as `openclaw-chat-only` or `openclaw-work`.
- If your OpenClaw workflow needs additional network destinations beyond the
  built-in preset, create a preset file with the extra allow-list entries and
  use `--preset-file` together with `--preset`.
- For the exact built-in template contents, see
  [rootfs.md#built-in-templates](rootfs.md#built-in-templates).
