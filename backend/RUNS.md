# Run engine foundation

The run engine is the shared durable execution record for chat, Agents, Tools
and future Workflows. Chat is the first production path integrated with it.

## Model

```text
Run
├── Step: knowledge retrieval
├── Step: model generation
└── Event stream ordered by per-run sequence
```

Run and step statuses are:

```text
queued → running → succeeded
                 ↘ failed
                 ↘ waiting_approval → running
                 ↘ cancelled
                 ↘ timed_out
```

Terminal states cannot transition again. The repository enforces transitions
while holding a row lock so concurrent workers cannot both finalize a run.

## Durable jobs

`worker_jobs` is a PostgreSQL-backed queue written in the same transaction as a
new run. Workers claim jobs with `FOR UPDATE SKIP LOCKED`, renew a lease while a
handler runs, and recover expired leases after a crash. Retries use exponential
backoff and each job can carry a unique deduplication key.

Handlers must make external side effects idempotent. Queue delivery itself is
at-least-once; a lease prevents normal duplication but cannot prove an external
system did not accept a request immediately before a worker crashed.

## API

- `GET /api/runs?workspace={id}`
- `GET /api/runs/{id}`
- `GET /api/runs/{id}/steps`
- `GET /api/runs/{id}/events?after={sequence}`
- `GET /api/runs/{id}/stream?after={sequence}`
- `POST /api/runs/{id}/cancel`

The stream uses the per-run event sequence as a resume cursor. It closes with a
`done` event after the run reaches a terminal state.

## Sensitive data

Run metadata stores resource identifiers, model names, counts and references.
Chat prompt and answer text remain in the conversation tables instead of being
copied into operational events. Credentials must be resolved just in time by a
handler and must never be written to a run, step, event or job payload.
