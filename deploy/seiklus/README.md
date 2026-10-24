# Seiklus deployment

Runs this fork's `seiklus` branch for one event at
`https://seiklus.hypersuper.link`. Every push to `seiklus` builds
`ghcr.io/lauriliivamagi/zonventure:seiklus`
([workflow](../../.github/workflows/seiklus-image.yml)); a Portainer
stack runs it from [`compose.yaml`](compose.yaml).

## Portainer stack

- Build method: Repository
- Repository URL: `https://github.com/lauriliivamagi/zonventure`
- Reference: `refs/heads/seiklus`
- Compose path: `deploy/seiklus/compose.yaml`
- Authentication off (the repository is public); automatic updates off.

## Variables

Set these on the stack. Keep the secrets in a password manager too.

| Variable | Required | Value |
| --- | --- | --- |
| `POSTGRES_PASSWORD` | yes | `openssl rand -hex 24`. Hex only, because it goes into a URL. Fixed at the first deploy: Postgres reads it once into an empty volume, so changing it later breaks the app's connection. |
| `ZV_SEED_ADMIN` | yes | `<username>:<password>` for the first admin. Use a non-obvious username and a random password, then change the password in the app at the first sign-in. |
| `ZV_TOTP_KEY` | yes | `openssl rand -base64 32`. Wraps two-factor secrets; losing it forces everyone to re-enrol. |
| `TUNNEL_NETWORK` | yes | The Docker network the tunnel container is on. |
| `ZV_TRUSTED_PROXIES` | yes | That network's subnet. |
| `ZV_IMAGE_TAG` | no | Default `seiklus`; `sha-<short>` to pin a build. |
| `ZV_PUBLIC_URL` | no | Default `https://seiklus.hypersuper.link`. |

## Tunnel

Route the hostname to `http://seiklus-app:8080`. The app publishes no
host port; it is reachable only through the tunnel network.

## Update

1. Back up (below).
2. Stack → Pull and redeploy, with "Re-pull image" on.
3. Check that `https://seiklus.hypersuper.link/api/health` shows the new
   `version` (`git describe --tags --always` of the pushed commit).

## Rollback

Set `ZV_IMAGE_TAG=sha-<short>` to a previous build and update the stack.
Database migrations do not roll back; restore a backup if one ran.

## Backup

Open a `/bin/sh` console on the `db` container and run:

```sh
pg_dump -U zv -d zv --clean --if-exists --no-owner --no-privileges \
  | gzip > /backups/zv-$(date +%F-%H%M).sql.gz
```

The dumps contain players' names and positions; delete them after the
event. Restore as in [operations.md → Restore](../../docs/operations.md#restore).

## Event day

No redeploys from the day before until the event is over. Watchtower
skips both containers (`com.centurylinklabs.watchtower.enable=false`).

## After the event

1. Delete the event and its teams in the app.
2. Delete `/backups/*.sql.gz` in the `db` container.
3. Remove the tunnel route, or stop the stack, until the next use.
