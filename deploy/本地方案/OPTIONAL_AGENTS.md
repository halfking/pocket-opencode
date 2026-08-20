# Optional local agents

The default local stack intentionally starts only `pocketd` and the frontend. `opencode-manager` and `opencode-plugin` remain host-side optional agents because they need a real OpenCode runtime and instance credentials.

When OpenCode is available on port `4096`, use the host-side agent instructions in their respective READMEs and point them at:

```text
ws://127.0.0.1:8088/plugin/ws
```

They are not prerequisites for the core Pocket + PostgreSQL test gate.
