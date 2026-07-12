# Claude OAuth Setup

The agent-claude runner uses Claude Code CLI which requires OAuth credentials.
Credentials are stored on a PVC (`agent-claude`) mounted at `/home/claude/.claude`.

## Prerequisites

- PVC `agent-claude` exists in the target namespace
- Apply if missing: `cd agent/agent/claude/k8s && make apply`

## Setup (one-time per namespace)

Run a temporary pod with the PVC mounted:

```bash
kubectlquant -n <NAMESPACE> run claude-setup --rm -it \
  --image=node:22-alpine \
  --overrides='{
    "spec": {
      "containers": [{
        "name": "claude-setup",
        "image": "node:22-alpine",
        "stdin": true,
        "tty": true,
        "command": ["/bin/sh"],
        "volumeMounts": [{
          "name": "claude-data",
          "mountPath": "/home/claude/.claude"
        }]
      }],
      "volumes": [{
        "name": "claude-data",
        "persistentVolumeClaim": {
          "claimName": "agent-claude"
        }
      }]
    }
  }'
```

Inside the pod:

```sh
export HOME=/home/claude
npm install -g @anthropic-ai/claude-code
claude login
# Follow browser auth flow
ls -la /home/claude/.claude/
# Verify: .credentials.json and settings.json exist
exit
```

Pod auto-deletes (`--rm`). Credentials persist on the PVC.

## Token Refresh

Claude Code refreshes OAuth tokens automatically. The `.credentials.json` file contains:

- `accessToken` — short-lived, auto-refreshed
- `refreshToken` — long-lived
- `expiresAt` — expiry timestamp

If the refresh token expires (rare), re-run the setup steps above.

## Verify

Check PVC contents from a temp pod:

```bash
kubectlquant -n <NAMESPACE> run claude-check --rm -it \
  --image=alpine \
  --overrides='{
    "spec": {
      "containers": [{
        "name": "claude-check",
        "image": "alpine",
        "stdin": true,
        "tty": true,
        "command": ["/bin/sh"],
        "volumeMounts": [{
          "name": "claude-data",
          "mountPath": "/home/claude/.claude"
        }]
      }],
      "volumes": [{
        "name": "claude-data",
        "persistentVolumeClaim": {
          "claimName": "agent-claude"
        }
      }]
    }
  }'
```

```sh
cat /home/claude/.claude/.credentials.json | head -c 100
# Should show claudeAiOauth JSON
exit
```

## Namespaces

Repeat setup for each namespace where the agent runs:

- `dev` — development/testing
- `prod` — production
