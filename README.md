# manapool-jobs

Daily watcher for [Mana Pool careers](https://manapool.com/jobs). It records every posting it has seen in Postgres. A **new** slug or a listing that flips from closed back to **Now Hiring** is parsed through the cluster LLM and posted to Discord.

A quiet day is a no-op: no model call, no Discord message.

MIT licensed.

## What it does

1. Fetches `https://manapool.com/jobs` (public HTML, no ManaPool token, no Tor).
2. Parses each card in Go: slug, title, department, location, hiring badge.
3. Compares slugs to `mtg.manapool_job_postings`.
4. **Empty table (first tick):** writes every current listing, calls the LLM for a summary, does **not** page Discord. That is the bootstrap so today's jobs are not treated as news.
5. **Later ticks:**
   - Known and unchanged → `last_seen_at` only.
   - New slug, or a known slug that reopened → fetch the detail page, `POST` the text to the LLM proxy, upsert the row.
   - If that listing is **Now Hiring**, `POST` `@everyone` plus an embed to the Discord bridge.

Closed listings are stored so a later reopen can page. A brand-new closed listing is recorded and not alerted.

The binary is a one-shot. Kubernetes (or cron, or launchd) starts it; it exits.

## Architecture (cluster)

This is the live homelab path. Flux CronJob `manapool-jobs` in namespace `mtg`, image `manapool-jobs:v1`, schedule `0 12 * * *` UTC (08:00 Eastern).

```mermaid
flowchart TD
  cron["CronJob manapool-jobs<br/>daily 12:00 UTC"]
  jobs["GET manapool.com/jobs"]
  parse["Go listing parse<br/>slug / title / hiring badge"]
  pg[("Postgres app<br/>mtg.manapool_job_postings")]
  detail["GET /jobs/&lt;slug&gt;"]
  llm["POST llm.llm.svc<br/>/v1/chat<br/>X-LLM-Caller: manapool-jobs"]
  bridge["POST discord.discord.svc<br/>/v1/messages<br/>route=default"]
  webhook["Discord webhook<br/>channel"]

  cron --> jobs --> parse --> pg
  parse -->|"new or reopened"| detail --> llm --> pg
  llm -->|"hiring and not bootstrap"| bridge --> webhook
  parse -->|"known, unchanged"| pg
```

Nothing in this process talks to `api.openai.com` or `discord.com`. Those hops stay inside the cluster proxy and the Discord bridge.

| Hop | Cluster default | Why |
|-----|-----------------|-----|
| Listing + detail HTML | `https://manapool.com/jobs` | Public careers page |
| Seen-state | `postgres-rw.postgres.svc.cluster.local` / db `app` | Survives pod restarts |
| Job prose | `http://llm.llm.svc.cluster.local` `gpt-4o-mini` | Metered, capped |
| Alert | `http://discord.discord.svc.cluster.local` route `default` | Same channel as the other shop pages |

## Environment

| Variable | Default | Notes |
|----------|---------|--------|
| `JOBS_URL` | `https://manapool.com/jobs` | Listing page |
| `DATABASE_URL` | unset | If set, used as-is and the `POSTGRES_*` vars are ignored |
| `POSTGRES_HOST` | `postgres-rw.postgres.svc.cluster.local` | |
| `POSTGRES_PORT` | `5432` | |
| `POSTGRES_DATABASE` | `app` | |
| `POSTGRES_USER` | `postgres` | |
| `POSTGRES_PASSWORD` | empty | |
| `LLM_URL` | `http://llm.llm.svc.cluster.local` | Base URL. The process calls `{LLM_URL}/v1/chat` |
| `LLM_MODEL` | `gpt-4o-mini` | |
| `DISCORD_URL` | `http://discord.discord.svc.cluster.local` | Base URL. The process calls `{DISCORD_URL}/v1/messages` |
| `DISCORD_ROUTE` | `default` | Bridge route name, not a Discord channel id |

The LLM and Discord clients speak **this repo's cluster APIs**, not raw OpenAI and not a Discord webhook URL. Pointing `LLM_URL` at `https://api.openai.com` or `DISCORD_URL` at a webhook will 404.

## Run outside Kubernetes

You need four things the CronJob gets from the cluster: a network path to `manapool.com`, Postgres, something that implements `POST /v1/chat`, and something that implements `POST /v1/messages`. Two workable setups:

### A. Laptop, services still in the cluster

Use this when you have `kubectl` to the homelab. The binary runs on your machine; Postgres, LLM, and Discord stay where they are.

```bash
kubectl port-forward -n postgres svc/postgres-rw 5432:5432
kubectl port-forward -n llm svc/llm 8080:80
kubectl port-forward -n discord svc/discord 8081:80
```

Then, from this repo:

```bash
go test ./...
go run .

export DATABASE_URL='postgres://postgres:PASSWORD@127.0.0.1:5432/app'
export LLM_URL='http://127.0.0.1:8080'
export DISCORD_URL='http://127.0.0.1:8081'
go run .
```

First local run against an empty `mtg.manapool_job_postings` bootstraps and will not page. Delete a hiring row (or wait for a real new job) if you want to see a Discord message.

Do **not** `kubectl create job --from=cronjob/manapool-jobs` to test. That is the same rule as the other CronJobs.

### B. No cluster at all

1. **Postgres.** Any 14+ instance. The process creates schema `mtg` and table `manapool_job_postings` on startup.

```bash
docker run --rm -d --name mp-jobs-pg -e POSTGRES_PASSWORD=dev -p 5432:5432 postgres:16
export DATABASE_URL='postgres://postgres:dev@127.0.0.1:5432/postgres'
```

2. **LLM.** The watcher `POST`s JSON to `{LLM_URL}/v1/chat` with header `X-LLM-Caller: manapool-jobs`:

```json
{
  "model": "gpt-4o-mini",
  "json": true,
  "max_tokens": 450,
  "system": "Extract the Mana Pool job posting ...",
  "prompt": "<detail page text>"
}
```

It expects `{"ok": true, "json": { ...JobParse }}` or a `text` field that is that JSON object. Fields: `title`, `department`, `location`, `hiring`, `compensation`, `how_to_apply`, `summary`, `highlights`.

A 30-line shim that wraps OpenAI's Chat Completions API is enough. Example:

```python
# llm_shim.py — python3 llm_shim.py
# LLM_URL=http://127.0.0.1:8090
import json, os, urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(n))
        payload = {
            "model": body.get("model") or "gpt-4o-mini",
            "messages": [
                {"role": "system", "content": body.get("system") or ""},
                {"role": "user", "content": body.get("prompt") or ""},
            ],
            "max_tokens": int(body.get("max_tokens") or 450),
            "response_format": {"type": "json_object"} if body.get("json") else None,
        }
        payload = {k: v for k, v in payload.items() if v is not None}
        req = urllib.request.Request(
            "https://api.openai.com/v1/chat/completions",
            data=json.dumps(payload).encode(),
            headers={
                "Authorization": "Bearer " + os.environ["OPENAI_API_KEY"],
                "Content-Type": "application/json",
            },
        )
        with urllib.request.urlopen(req) as resp:
            data = json.load(resp)
        text = data["choices"][0]["message"]["content"]
        parsed = json.loads(text) if body.get("json") else None
        out = json.dumps({"ok": True, "text": text, "json": parsed}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(out)

HTTPServer(("127.0.0.1", 8090), H).serve_forever()
```

3. **Discord.** The watcher `POST`s to `{DISCORD_URL}/v1/messages`:

```json
{
  "route": "default",
  "content": "@everyone New Mana Pool job: **Title**",
  "allowed_mentions": {"parse": ["everyone"]},
  "embeds": [{ "title": "...", "url": "...", "description": "...", "fields": [] }]
}
```

A webhook adapter (ignore `route`, forward `content` + `embeds`):

```python
# discord_shim.py — DISCORD_URL=http://127.0.0.1:8091
import json, os, urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

HOOK = os.environ["DISCORD_WEBHOOK_URL"]

class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(n) or b"{}")
        payload = {k: body[k] for k in ("content", "embeds", "allowed_mentions") if k in body}
        req = urllib.request.Request(
            HOOK, data=json.dumps(payload).encode(),
            headers={"Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req) as resp:
            resp.read()
        self.send_response(204)
        self.end_headers()

HTTPServer(("127.0.0.1", 8091), H).serve_forever()
```

4. **Run once, then schedule it.**

```bash
export DATABASE_URL='postgres://postgres:dev@127.0.0.1:5432/postgres'
export LLM_URL='http://127.0.0.1:8090'
export DISCORD_URL='http://127.0.0.1:8091'
go run .
```

Outside Kubernetes you own the schedule. Daily is enough:

```cron
0 8 * * * cd /path/to/manapool-jobs && DATABASE_URL=... LLM_URL=... DISCORD_URL=... ./manapool-jobs
```

On macOS, a `StartCalendarInterval` LaunchAgent at 08:00 local is the same idea.

## Build

```bash
go test ./...
go build -o manapool-jobs .
# cluster image
docker build --platform linux/arm64 -t 987798005511.dkr.ecr.us-east-1.amazonaws.com/manapool-jobs:v1 .
```

Flux pin lives in `k3s-lab` at `clusters/homelab/apps/mtg/manapool-jobs-cronjob.yaml`. A local `kubectl apply` is not the deploy.

## Decision table

| Table state | Listing badge | Action |
|-------------|---------------|--------|
| empty | any | insert + LLM, no Discord |
| missing slug | Now Hiring | insert + LLM + Discord |
| missing slug | Applications Closed | insert + LLM, no Discord |
| known closed → Now Hiring | Now Hiring | update + LLM + Discord |
| known, same hiring flag | any | touch `last_seen_at` only |
