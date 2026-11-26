# ADR-038: Shared Inference Gateway

Status: Accepted (cq-gpa.4)

## Context
Six agent processes each called llama.cpp directly. The server has 2 slots (`/props` -> `total_slots`), and each process had its own per-process governor, so requests queued inside llama.cpp and client timeouts (which started at send time) expired under load.

## Decision
The supervisor hosts a small HTTP reverse proxy (`internal/inference`) on a loopback port (`127.0.0.1:0`) and passes its URL to agents via `CIVQUEST_INFERENCE_URL`. `civquest-agent` then builds its `system2.Client` with `WithInferenceProxy`, which points `baseURL` at the proxy and disables the client-side timeout.

- Concurrency: `GET /props` `total_slots` at startup; fallback to `inference_governor.max_concurrent_llm_requests`, minimum 1.
- Queue: priority heap, tactical > dialogue > ambient, FIFO within a class. Priority travels in the `X-CQ-Priority` header (set from `inference.WithPriority(ctx)`; defaults: structured/raw completions tactical, creative dialogue dialogue).
- No queue timeout. A wait ends only on grant, caller disconnect/context cancel, gateway close, or (ambient only) exceeding `StaleAge`, which drops the request (HTTP 429, counted as dropped).
- The per-request deadline (`request_timeout_seconds`) starts only after slot acquisition.
- Metrics: queued, in-flight, dropped, completed at `GET /cq/inference/stats`.

## Alternatives rejected
- Cross-process semaphore (flock/SysV): no priorities, no stale drop, no shared metrics, awkward crash cleanup.
- Unix socket: equivalent, but a loopback port needs no path management and works with the existing HTTP client unchanged.

## Consequences
The supervisor is a single point of failure for inference; if no upstream is configured the proxy is off and agents call llama.cpp directly as before. The per-process governor still applies and should not be set below the slot count. Callers of ambient work (lore, contemplation) must tag their context with `inference.Ambient` to become droppable.
