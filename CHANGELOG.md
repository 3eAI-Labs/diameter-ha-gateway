# Changelog

All notable changes to diameter-ha-gateway are documented in this file.
Format loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[SemVer](https://semver.org/) (pre-1.0: a minor version may be breaking).

Versions up to 0.9.4 were released privately; 0.10.0 is the first public release. The entries below
describe what changed for an installation of 0.9.4.

## [Unreleased]

## [0.10.0] — 2026-10-17

### Licensing and packaging

- The gateway is licensed under AGPL-3.0-only; contributions require the
  [Contributor Licence Agreement](CLA.md).
- The S6a/S13 wire contract moved to its own Apache-2.0 module,
  `github.com/3eAI-Labs/diameter-ha-gateway-client` (package `diameterclient`). It replaces
  `github.com/3eAI-Labs/diameter-ha-gateway/pkg/api/diameter/v1`; its CHANGELOG lists the schema
  changes.
- From this release, image and chart are published to GHCR: `ghcr.io/3eai-labs/diameter-ha-gateway` and
  `oci://ghcr.io/3eai-labs/charts/diameter-ha-gateway` (chart 0.2.0); the chart defaults to the GHCR
  image.
- The image carries the licence texts and NOTICE files of the gateway, of every compiled Go module
  and of every npm package bundled into the admin UI, under
  `/usr/share/licenses/diameter-ha-gateway/`.

### Breaking

- **NATS: core request/reply only.** The gateway no longer uses JetStream. In 0.9.4 application-
  initiated requests (S6a, Gx, SGd) were subscribed and answered through JetStream, so no reply
  reached an application, and every reply was JSON-encoded twice. A JetStream stream must not
  capture the gateway's subjects: it answers each request with its publish acknowledgement.
- **No application-initiated PUR or NOR.** `diameter.s6a.pur` and `diameter.s6a.nor` carry only an
  MME's requests to the application (TS 29.272 §7.2.13, §7.2.17). In 0.9.4 the gateway also
  subscribed to them and sent an MME's own PUR back towards the MME.
- **Subscription-Data follows TS 29.272.** Every vendor-10415 AVP takes its code, type and M-bit
  from Table 7.3.1/1. 11 AVP codes were wrong (among them DSR-Flags and DSA-Flags swapped,
  V2X-Subscription-Data, Service-Gap-Time and the MDT area identities), the M-bit was set on every
  AVP, Visited-Network-Identifier was sent as vendor 0 and MIP6-Agent-Info as vendor 10415.
  Header-Compression and eDRX-Subscription-Data are removed (neither is in the specification);
  eDRX is sent as eDRX-Cycle-Length and Paging-Time-Window. AESE-Communication-Pattern and
  Monitoring-Event-Configuration are no longer sent: their encoding could not be valid.
- **Groups missing a mandatory AVP are not sent.** A Subscription-Data group whose mandatory child
  is absent from the application's reply (for example an APN-Configuration without PDN-Type) is
  left out and logged. Mandatory children with value 0 are now sent.
- **Reset (RSR) carries User-Id**, a leading part of the IMSI (TS 29.272 §7.3.50), instead of
  User-Name.
- **Admin authentication.** With `admin.auth.enabled`, every `/api/admin/v1` request needs a bearer
  token verified against `admin.auth.jwks_url`, with `issuer` and `audience` checked; in 0.9.4 the
  flag was only checked at start-up and the API was open. In the chart's `values.schema.json`,
  `config.admin.auth` with `enabled: true` now requires `jwks_url`, `issuer` and `audience`, so
  values written for chart 0.1.1 need the two new fields.

### Fixed

- **S13 works.** The S13 application was in no dictionary, so an ECR failed to parse and the
  connection was lost. The ECA carries an Experimental-Result (e.g. 5422) when the EIR reports one.
- **AVP values, not debug strings.** String AVPs were read with go-diameter's debug format: IMSI,
  IMEI, Session-Id, Origin-Host and realms reached applications as `UTF8String{…},Padding:…`;
  answers to application-initiated requests never matched; no CCR matched a configured
  Service-Context-Id. Service-Selection is decoded as an IETF AVP.
- **Answers carry the mandatory AVPs.** Every answer has Session-Id, Origin-Host and Origin-Realm,
  and echoes Auth-Session-State (S6a/S13) or Auth-Application-Id, CC-Request-Type and
  CC-Request-Number (CCA). In 0.9.4 a ULA could carry only the Result-Code.
- **Errors reach the application.** For application-initiated requests the reply carries the
  peer's Experimental-Result-Code; 0.9.4 reported such an answer as 2001.
- **SGd requests are sent once.** SMSC-initiated requests are subscribed with a queue group; with N
  replicas each was sent N times. A request that is not valid JSON is answered 5004.
- **NATS TLS is applied.** `nats.tls` was parsed and ignored.

### Security

- A peer's identity is bound at CER only. In 0.9.4 any authorised peer could put another peer's
  Origin-Host in a request and take over that peer's application-initiated traffic.
- An answer is accepted only on the connection its request was sent on.
- A Gx session stays bound to the PCEF that opened it; another peer's CCR can neither move nor end
  it.

### Known limitations

See the [README](README.md#known-limitations). New in this release: gateway-initiated requests
reach directly connected peers only, a consequence of binding identities to connections.

[Unreleased]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.10.0...HEAD
[0.10.0]: https://github.com/3eAI-Labs/diameter-ha-gateway/releases/tag/v0.10.0
