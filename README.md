# diameter-ha-gateway

A Diameter gateway that terminates 4G/LTE Diameter interfaces and hands each request to an
application over NATS. An HSS, an EIR, a PCRF or an OCS is written as an ordinary NATS service;
the gateway handles the Diameter peer connections, the base protocol and the encoding.

```
 MME, PGW, IMS          diameter-ha-gateway (N replicas)           applications
┌──────────────┐  Diameter  ┌──────────────────────┐  NATS core   ┌──────────────┐
│ S6a, S13     │◄──────────►│ peers, CER/DWR/DPR   │ request/reply│ HSS, EIR     │
│ Gx, Gy, Ro   │ TCP / SCTP │ AVP ⇄ JSON           │◄────────────►│ PCRF, OCS    │
│ SGd, S6c     │  (TLS opt.)│ admin API + UI       │ queue groups │ SMSC         │
└──────────────┘            └──────────────────────┘              └──────────────┘
```

Licensed under the [GNU Affero General Public License v3.0](LICENSE) (AGPL-3.0-only). The NATS wire
contract for S6a and S13 is a separate module under the Apache License 2.0,
[diameter-ha-gateway-client](https://github.com/3eAI-Labs/diameter-ha-gateway-client), so an
application can use it under any licence.

**Status: pre-1.0.** The S6a and S13 encoding is checked against 3GPP TS 29.272 V17.8.0; the other
applications are not checked against their specifications yet (see
[Known limitations](#known-limitations)).

## What it does

| Application | Peer | Requests from the network | Requests from an application |
|-------------|------|---------------------------|------------------------------|
| S6a (TS 29.272) | MME ⇄ HSS | ULR, AIR, PUR, NOR | CLR, IDR, DSR, RSR |
| S13 (TS 29.272) | MME ⇄ EIR | ECR | — |
| Gx (TS 29.212) | PCEF ⇄ PCRF | CCR | RAR, ASR |
| Gy (RFC 4006, TS 32.299) | PGW ⇄ OCS | CCR | RAR, ASR |
| Ro (RFC 4006, TS 32.299) | IMS ⇄ OCS | CCR | RAR, ASR |
| SGd (TS 29.338) | MME ⇄ SMSC | OFR, TFR | TFR |
| S6c (TS 29.338) | SMSC ⇄ HSS; ALR also MME ⇄ SMSC | SRR, ALR, RDR | SRR, ALR, RDR |

- **Requests from the network** are published as a NATS request on `diameter.<app>.<cmd>`
  (`diameter.s6a.ulr`, `diameter.gx.ccr`, …); the application's reply becomes the Diameter answer.
  Gx, Gy and Ro CCRs share one command code; the gateway picks the subject from the
  Application-Id (Gx) and the Service-Context-Id (Gy: `32251@3gpp.org`; Ro: `32260@3gpp.org`,
  `32275@3gpp.org` by default).
- **Requests from an application** are NATS requests to the gateway (`diameter.s6a.idr`,
  `diameter.gx.rar`, `diameter.sgd.tfr.request`, …). The gateway sends the Diameter request to the
  peer the application names and replies with the peer's result. S6c and SGd share the
  `diameter.sgd.*` subjects; an S6c or SGd request without `destination_host` (and, for TFR,
  `mme_name`) goes to any connected peer.
- **Gx, Gy and Ro sessions** are bound to the peer whose CCRs carry them, until its
  CCR-Termination or disconnect. The PCRF's RAR and ASR (`diameter.gx.rar`, `diameter.gx.asr`) and
  the OCS's (`diameter.gy.rar`, `diameter.gy.asr`, `diameter.ro.rar`, `diameter.ro.asr`) go back
  over that peer's connection. A request for a session without a binding goes to `origin_host`
  (Gx), or to `destination_host`, else `origin_host` (Gy, Ro).
- **Base protocol (RFC 6733):** capabilities exchange with a peer whitelist (source IP and
  Origin-Host), watchdog (DWR/DWA), disconnect (DPR/DPA) and drain on shutdown, Failed-AVP on
  missing mandatory AVPs, Redirect-Host handling with a per-command retry policy, per-peer rate
  limiting (3004 TOO_BUSY), idle-peer cleanup, TLS with optional client certificates.
- **Scaling:** the gateway keeps no subscriber state. Replicas subscribe with NATS queue groups, so
  each application-initiated request is handled once.
- **Admin API and UI** on the admin port: `GET /api/admin/v1/peers`, `POST /api/admin/v1/config/reload`
  (re-reads the configuration; listener, identity, TLS, admin and `app_env` changes are refused
  and need a restart). Bearer tokens are verified against a JWKS endpoint, issuer and audience
  checked; the gateway refuses to start without authentication when `app_env` is `prod` or
  `stage`.
- **Metrics** in Prometheus format on port 8080, `/metrics`.

The subjects and JSON envelopes of S6a and S13 are documented in the client module. Gx, Gy, Ro and
S6c/SGd envelopes are defined in this repository (`internal/charging`, `internal/sms/sgd`) and are
not yet a published contract.

## Running it

Requirements: Go 1.25; Node.js for the admin UI (the image builds it with Node 24); a NATS server
(core NATS; JetStream is not used).

```bash
make build            # bin/diameter-gateway
make test             # unit and integration tests (embedded NATS server), race detector on
make image            # container image diameter-ha-gateway:dev (UI, binary, licence texts)
```

The gateway reads `./configs/config.yaml` from its working directory;
[`configs/config.yaml`](configs/config.yaml) is a commented development configuration. Ports:
3868 Diameter, 8081 admin API and UI, 8080 metrics.

A Helm chart is in [`deployments/helm/diameter-ha-gateway`](deployments/helm/diameter-ha-gateway);
its `values.schema.json` documents every setting.

Do not put the gateway's subjects in a JetStream stream: a stream that captures a request subject
answers the request with its publish acknowledgement, and the requester never sees the reply.

## Security

- `diameter.s6a.air` replies carry authentication vectors. Restrict the subject with NATS
  permissions, run NATS with TLS (`nats.tls`) and never log these payloads.
- Peers are accepted only from the configured whitelist; enable Diameter TLS (`server.tls`) outside
  a trusted network.
- See [SECURITY.md](SECURITY.md) to report a vulnerability.

## Known limitations

- **Gateway-initiated requests reach directly connected peers only.** CLR, IDR, DSR, RSR, RAR, ASR
  and SGd requests go to the peer the application names, over that peer's own connection; a peer
  reachable only through a Diameter relay or proxy cannot be addressed.
- **Most metrics are not exported.** Only `diameter_requests_total`,
  `diameter_request_duration_seconds` and the HTTP metrics of the admin API reach `/metrics`; the
  other counters the code records (peer state, redirects, rate limiting, per-application errors)
  are discarded. The metrics port is fixed at 8080.
- **Specification coverage.** S6a and S13 AVP codes, flags and types are tested against TS 29.272
  Table 7.3.1/1. AVPs that TS 29.272 re-uses from TS 29.212, 29.214, 29.229, 29.061 and 32.299, and
  Gx, Gy and Ro have been audited against TS 29.212, 32.299 and RFC 8506 (see the CHANGELOG for what
  is fixed); S6c and SGd follow TS 29.338 V17.2.0. Many optional AVPs of Gx, Gy and Ro are not
  passed to applications.
- **SCEF features are not supported.** AESE-Communication-Pattern and
  Monitoring-Event-Configuration (TS 29.336) are not sent in Subscription-Data.
- **Incomplete groups are dropped.** A Subscription-Data group whose mandatory child is missing
  from the application's reply is not sent, and the omission is logged.
- **Fixed timeouts.** The S6a/S13 NATS request timeout is 2 s; `nats.connect_timeout` and
  `nats.request_timeout` are not used.
- **In-memory state per replica.** Pending gateway-initiated requests, Gx, Gy and Ro
  session-to-peer bindings and the redirect cache live in one replica's memory and are lost when
  it stops.
- **Gy and Ro redirects are not followed.** A Gy or Ro client answering a RAR or ASR with 3006
  (Redirect-Host) is reported to the OCS with that result code only; the Redirect-Host is not passed
  on and the gateway does not retry elsewhere.
- **Gx ASR is not part of TS 29.212.** `diameter.gx.asr` sends an RFC 4006 Abort-Session-Request,
  which a PCEF need not support; TS 29.212 ends an IP-CAN session with a RAR, or a CCA to a CCR
  update, carrying `session_release_cause` (§4.5.9).

## Contributing

Pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md). Contributions require the
[3eAI Labs Contributor Licence Agreement](CLA.md). Changes are listed in
[CHANGELOG.md](CHANGELOG.md); the design is summarised in
[docs/ARCHITECTURE_OVERVIEW.md](docs/ARCHITECTURE_OVERVIEW.md).

## Licence

Copyright 2026 3eAI Labs Ltd. [AGPL-3.0-only](LICENSE); see [NOTICE](NOTICE) for third-party
components. A commercial licence is available from 3eAI Labs.
