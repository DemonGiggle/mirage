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

The example preset `examples/presets/openclaw-offline.yaml` is useful when you
want a reviewed offline policy plus the recommended `openclaw-developer`
rootfs level and a default working directory of `/workspace`.

Install the package inside the generated rootfs:

```bash
./bin/mirage run \
  --preset-file ./examples/presets/openclaw-allow-all.yaml \
  -- npm i -g openclaw
```

Run the onboarding flow:

```bash
./bin/mirage run \
  --preset-file ./examples/presets/openclaw-allow-all.yaml \
  -- openclaw onboard
```

Start the local OpenClaw gateway on port `18789`:

```bash
./bin/mirage run \
  --preset-file ./examples/presets/openclaw-allow-all.yaml \
  -- openclaw gateway --port 18789
```

### Notes

- If you want a smaller or stricter installation environment, choose a narrower
  rootfs level such as `openclaw-chat-only` or `openclaw-work`.
- Prefer `examples/presets/openclaw-offline.yaml` or
  `examples/network-policies/offline.yaml` for local-only work, and switch to
  `examples/presets/openclaw-allow-all.yaml` or
  `examples/network-policies/allow-all.yaml` only when the workflow truly needs
  the host network stack.
- For the exact built-in template contents, see
  [rootfs.md#built-in-templates](rootfs.md#built-in-templates).
