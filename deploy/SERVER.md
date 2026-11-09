# League server runbook

k3s (single node) in an LXD VM on mce-pc-server, public through Tailscale Funnel
to NodePort 30080. Skeleton: nothing below has been run yet. Fill in the real
URL and any changes as each step is done.

## Setup checklist (Task 10)

- [x] 1. On mce-pc-server (2026-11-09, as soren, no sudo needed): create the VM and the backup mount.
  Backups live in `~soren/league-backups` on the host rather than `/srv`, because soren has no
  passwordless sudo there.

  ```sh
  lxc launch ubuntu:24.04 league --vm -c limits.cpu=4 -c limits.memory=6GiB -d root,size=40GiB
  mkdir -p ~/league-backups
  lxc config device add league backups disk source=$HOME/league-backups path=/backups
  lxc exec league -- df -h /backups   # shows the mount
  ```

- [x] 2. In the VM (2026-11-09, k3s v1.37.2+k3s1): install k3s without Traefik.

  ```sh
  lxc exec league -- sh -c 'curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--disable traefik --write-kubeconfig-mode 600" sh -'
  lxc exec league -- k3s kubectl get nodes   # Ready
  ```

  Tailscale 1.104.5 is installed in the VM (`curl -fsSL https://tailscale.com/install.sh | sh`).

- [ ] 3. Tailnet policy (admin console): add `tag:league` with no outbound grants to
  other nodes, and a `nodeAttrs` entry with `"attr": ["funnel"]` for `tag:league`. Then in the VM:

  ```sh
  tailscale up --advertise-tags=tag:league --hostname=league
  ```

- [ ] 4. Funnel to the NodePort.

  ```sh
  tailscale funnel --bg 30080
  ```

  Funnel URL: `https://league.<tailnet>.ts.net` (fill in). Log one request and check
  whether `X-Forwarded-For` is sent; set `TRUST_PROXY` in `deploy/k8s/api.yaml` to match.

## Deploy (Task 11)

Create the secrets by hand. Real values never go in git, shell history files, or issues.

```sh
kubectl apply -f deploy/k8s/namespace.yaml

kubectl -n league create secret generic league-db \
  --from-literal=POSTGRES_PASSWORD='<db password>'

kubectl -n league create secret generic league-api \
  --from-literal=DATABASE_URL='postgres://postgres:<db password>@postgres:5432/league' \
  --from-literal=SMTP_USER='<game gmail address>' \
  --from-literal=SMTP_PASS='<gmail app password>'
```

Apply everything and check:

```sh
kubectl apply -f deploy/k8s/
kubectl -n league get pods        # postgres, sim, api Running and Ready
curl https://league.<tailnet>.ts.net/healthz
```

## Roll out a new image

Images are `:latest` with `imagePullPolicy: Always`, so a restart pulls the newest.
(For a controlled rollout pin `sha-<7>` in the manifests instead.)

```sh
kubectl -n league rollout restart deploy/api deploy/sim
kubectl -n league rollout status deploy/api
```

## Logs

```sh
kubectl -n league logs deploy/api -f
kubectl -n league logs deploy/sim -f
kubectl -n league logs statefulset/postgres
kubectl -n league logs job/<job name>      # a backup run
```

## Backups

The CronJob runs at 03:30 and writes `/backups/league-<date>.dump` in the VM
(host: `/srv/league-backups`), deleting dumps older than 14 days.

Drill (run once after deploy, and after any change to the backup):

```sh
kubectl -n league create job --from=cronjob/backup drill
kubectl -n league logs job/drill
ls -l /srv/league-backups          # on the host: a new dump
```

Restore into a scratch database (never straight over prod):

```sh
kubectl -n league exec -it postgres-0 -- createdb -U postgres league_restore_check
kubectl -n league exec -i postgres-0 -- pg_restore -U postgres -d league_restore_check --no-owner < /srv/league-backups/league-<date>.dump
kubectl -n league exec postgres-0 -- psql -U postgres -d league_restore_check -Atc "select count(*) from teams"
kubectl -n league exec postgres-0 -- dropdb -U postgres league_restore_check
```

(Run the `pg_restore` line from the VM, where `/backups` is mounted; use
`/backups/league-<date>.dump` there.) Compare the counts with the live `league` database.
