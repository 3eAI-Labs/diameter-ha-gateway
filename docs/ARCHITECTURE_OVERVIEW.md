# Architecture overview

diameter-ha-gateway is a Diameter endpoint on one side and a NATS client on the other. It owns the
Diameter peer connections and the base protocol; everything an HSS, EIR, PCRF, OCS or SMSC decides
is made by an application behind NATS. This document describes how the code is organised and how a
request moves through it.

## Components

| Package | Role |
|---------|------|
| `cmd/diameter-gateway` | Start-up: configuration, NATS connection, peer manager, Diameter server, per-application handlers, admin API, metrics server, graceful shutdown. |
| `internal/diameter` | Diameter server (go-diameter state machine), peer manager, peer authorisation, per-peer rate limiter, Redirect-Host cache, dictionaries, S6a/S13 translation and the Subscription-Data encoder. |
| `internal/service` | S6a/S13 request handling towards NATS (`gateway.go`) and application-initiated S6a and Gx requests towards peers (`reverse.go`, `gx_reverse.go`). |
| `internal/charging` | Gx, Gy and Ro: a dispatcher that routes a CCR by Application-Id and Service-Context-Id, one handler and translator per application, and the Gx session registry (Session-Id → PCEF). |
| `internal/sms/sgd` | SGd (SMS over Diameter): handler for network requests, client for SMSC-initiated requests. |
| `internal/nats` | Core NATS client with a circuit breaker and optional TLS. |
| `internal/admin` | Admin HTTP API and the static admin UI, JWT authentication against a JWKS endpoint. |
| `internal/config` | Configuration loading (`./configs/config.yaml`) and hot reload. |
| `internal/platform` | Logging, audit events with IMSI masking, alarms, Prometheus metrics. |
| `web/` | Admin UI (React, built into `web/dist` and served by the admin API). |
| `deployments/` | Container image and Helm chart. |

The S6a/S13 wire types live in a separate Apache-2.0 module,
`github.com/3eAI-Labs/diameter-ha-gateway-client`, so applications do not link AGPL code.

## Request from the network

```
MME ──ULR──► server ──► admit gate ──► GatewayService ──NATS request──► application
                         (rate limit)    AVPs → JSON      diameter.s6a.ulr
MME ◄──ULA── server ◄── translator ◄──────────────────── NATS reply (S6aAnswer)
                         JSON → AVPs
```

1. The server accepts a request only from a peer that completed CER with an identity on the
   whitelist (source IP and Origin-Host).
2. The admit gate applies the per-peer rate limit; a rejected request gets 3004 (TOO_BUSY).
3. Missing mandatory AVPs are answered with 5005 and a Failed-AVP before anything reaches NATS.
4. The request is translated to JSON and sent as a core NATS request. A timeout or an open circuit
   breaker answers 3002 (UNABLE_TO_DELIVER); a reply that cannot be encoded answers 5012.
5. The reply is encoded into the answer. Session-Id, Origin-Host, Origin-Realm, the request's
   Proxy-Info AVPs and, for S6a/S13, Auth-Session-State are always set by the gateway. 3GPP result
   codes travel in Experimental-Result; a reply without a result code is answered 5012. A 3xxx
   answer carries the E flag, and the gateway's answers clear the T flag.

A request for a command the gateway does not serve is answered 3001 (COMMAND_UNSUPPORTED), one for
a base command under an unknown application 3007 (APPLICATION_UNSUPPORTED).

For Subscription-Data, every vendor-10415 AVP takes its code, type and M-bit from TS 29.272
Table 7.3.1/1 (`ts29272_avps.go`), and a group missing a mandatory child is left out and logged.

Gx, Gy and Ro share the CCR command code. The dispatcher recognises a Gx CCR by its Application-Id
(16777238); Gy and Ro share Application-Id 4 and are told apart by Service-Context-Id
(`charging.gy_context_ids`, `ro_context_ids`), matched with or without its release and operator
prefix (TS 32.299 §7.1.12). A CCR missing a mandatory AVP is answered 5005. A Gx CCR-Initial or -Update binds its Session-Id to the PCEF it came from, and
a CCR-Termination removes the binding, so a RAR or ASR from the PCRF can find the peer.

## Request from an application

```
application ──NATS request──► ReverseService ──CLR──► MME (by Destination-Host)
   diameter.s6a.clr          pending[Session-Id]
application ◄──NATS reply──── ReverseService ◄──CLA── MME
   result / experimental result
```

1. Each replica subscribes to the application-initiated subjects with the queue group
   `diameter-gw`, so one replica handles each request.
2. The target peer is named in the request (`header.source_host`) and must be connected to this
   replica; the request is sent over that peer's connection.
3. The request carries the P flag and a Session-Id of the form `<origin-host>;<high>;<low>;<kind>`
   (RFC 6733 §8.8). The waiter is stored by Session-Id with the connection it was sent on; an
   answer arriving on another connection is ignored, and an error answer without a Session-Id is
   matched by its hop-by-hop identifier.
4. A 3006 (REDIRECT_INDICATION) answer is followed to the Redirect-Host when the command's retry
   policy allows it: CLR, RSR and Gx RAR always, since applying them twice is harmless; IDR and
   Gx ASR only when the operator enables it (`redirect_idr_retry_enabled`,
   `redirect_asr_retry_enabled`); other commands never. The redirected request is a new request
   and carries no T-bit (RFC 6733 §3).
5. The reply carries the peer's Result-Code or Experimental-Result-Code. A timeout
   (`reverse_timeout`) answers 3002; a peer that disconnects fails its waiters at once.

## Peers

A peer is accepted at CER only from the whitelist (source IP and Origin-Host); any other peer is
disconnected without a CEA. The peer manager tracks each accepted connection from CER to
disconnect: it sends DWR on an idle connection and marks the peer down after three unanswered DWRs,
closes peers that send no application traffic within `idle_peer_timeout` when that is set (off by
default), and on shutdown sends DPR to every peer and waits for DPA. Peer identity is taken from the
CER; a later request's Origin-Host does not move a connection.

## State

The gateway stores no subscriber data. Per replica, in memory: pending application-initiated
requests, Gx session-to-peer bindings (evicted by idle time and absolute lifetime, after a minimum lifetime), the Redirect-Host
cache and rate-limiter buckets. A replica that stops loses them; the applications' NATS requests
time out and can be retried.

## Configuration and operations

- `./configs/config.yaml` (relative to the working directory; `/app/configs` in the image). The
  Helm chart renders it from `values.yaml`.
- `POST /api/admin/v1/config/reload` re-reads it. Listener, identity, TLS, admin and `app_env`
  changes are refused and need a restart.
- With `app_env` set to `prod` or `stage`, the gateway does not start unless admin authentication is
  enabled.
- Metrics on port 8080 (`/metrics`); see the README for which ones are exported today.
