# Dashboard

Self-hosted web dashboard for `analyser` runs: trends, per-branch comparison
and full drill-down across every run CI has pushed.

## Run it

```bash
cat > .env <<'EOF'
POSTGRES_PASSWORD=<a strong password>
DASHBOARD_ADMIN_TOKEN=<a strong random token>
EOF
docker compose up -d --build
```

Open http://localhost:8080 and sign in with `DASHBOARD_ADMIN_TOKEN`.

`POSTGRES_PASSWORD` is interpolated directly into `DATABASE_URL` as a
connection-string component (see `docker-compose.yml`). A password
containing `@`, `/`, `#` or `?` will produce a broken DSN — stick to
alphanumerics, or URL-encode it yourself if you need one of those characters.

The server also reads `DASHBOARD_ADDR` (default `:8080`) to change what
address it binds, independent of which host port `DASHBOARD_PORT` maps to it.

## Register a repo

```bash
curl -sX POST localhost:8080/api/admin/repos \
  -H "Authorization: Bearer $DASHBOARD_ADMIN_TOKEN" \
  -d '{"remote_url":"git@github.com:acme/widgets.git"}'
```

The response contains the repo's ingest token. **It is shown once** — store it
as a CI secret. Lost tokens are replaced, not recovered:
`POST /api/admin/repos/<id>/token`. To revoke a repo entirely (deleting it and
every run and finding it owns, invalidating its ingest token in the process),
use `DELETE /api/admin/repos/<id>`.

## Push from CI

```bash
analyser run . --dashboard-url https://dashboard.internal --dashboard-token "$ANALYSER_DASHBOARD_TOKEN"
```

Branch and commit are read from the checkout (the remote is not read at all —
the dashboard identifies a repo by its ingest token, not by remote URL). If
the dashboard is unreachable the CLI prints a warning and keeps its normal
exit code — a dashboard outage never fails a build.

## Exposing this beyond localhost

**The server speaks plain HTTP by design.** Both the admin token (which
grants access to every repo's data) and every repo's ingest token travel as
cleartext bearer headers on every request. Do not publish `DASHBOARD_PORT` to
a public interface, and do not point CI or a browser at this server over an
untrusted network as-is.

Before this is reachable off the host it is deployed on, put a
TLS-terminating reverse proxy (e.g. Caddy, nginx, or your cloud load
balancer) in front of it and only expose the proxy's HTTPS port. The Go
server itself does not and will not terminate TLS.

## Working on the frontend

```bash
cd internal/dashboard/web/ui
npm install
npm run dev     # Vite on :5173, proxying /api to a dashboard on :8080
npm test        # component tests
npm run build   # writes ../dist — COMMIT THE RESULT
```

`internal/dashboard/web/dist/` is a committed build artifact: `go build`
cannot run npm, so the binary embeds whatever is checked in. Rebuild and
commit it with any UI change.

## Notes

- Every branch that pushes is stored and viewable; the UI opens on `main`.
- Re-pushing the same commit overwrites that run rather than duplicating it.
- Retention is unlimited; there is no pruning job.
- The admin token gates the UI and all repo management. A repo's ingest token
  can only write that repo's runs.
