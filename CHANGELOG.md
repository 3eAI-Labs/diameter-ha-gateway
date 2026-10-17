# Changelog

All notable changes to diameter-ha-gateway are documented in this file.
Format loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[SemVer](https://semver.org/) (pre-1.0: a minor version may be breaking).

Versions up to 0.9.4 were released privately; 0.10.0 is the first public release. Its entry
describes what changed for an installation of 0.9.4.

## [Unreleased]

## [0.10.3] — 2026-10-17

Security release; chart 0.2.3. Every installation of 0.10.0–0.10.2 should upgrade. Before
upgrading, check that each of your peers has an entry in `peers` (source IP and Origin-Host): the
earlier releases accepted every peer, this one disconnects any peer without an entry. Configuration
keys and NATS subjects are unchanged; the Gx and Ro envelope changes under *Changed* affect only
CCRs that earlier releases could not answer.

### Security

- **The peer whitelist was not applied (0.10.0–0.10.2).** The gateway registered its CER handler
  with go-diameter's state machine, which does not accept a handler for CER and ignored it. Every
  peer that could open a connection received CEA 2001, and its requests — ULR, AIR and every other
  application request — were handed to the applications. The gateway now checks the CER against
  `peers` (source IP and Origin-Host) before the state machine answers it; any other peer is
  disconnected without a CEA. Until you upgrade, restrict the Diameter port to your peers' addresses
  at the network level.
  - A multi-homed SCTP peer is accepted only when every one of its addresses matches its `peers`
    entry, since the peer lists those addresses itself; whitelist such a peer with a CIDR.
  - DPR, DWA and DPA count for the peer their connection registered as at CER, not for the
    Origin-Host they carry, so one peer cannot disconnect another.
- **One AVP could disconnect a peer.** Six AVPs, each valid per specification, made the request's
  handler panic, and go-diameter closed the connection, dropping every other request on it:
  UE-SRVCC-Capability (ULR, NOR), Alert-Reason (NOR), Bearer-Operation, AN-GW-Status and
  PDN-Connection-ID (Gx CCR), and Event-Type (Ro CCR). Each is now decoded with its specification
  type. A request whose handler panics is answered 5012 (DIAMETER_UNABLE_TO_COMPLY) on a connection
  that stays up, and counted in `diameter_handler_panic_total`.

### Fixed

- **Application-initiated requests reach the peer.** No peer was ever registered, so CLR, IDR, DSR,
  RSR and Gx RAR/ASR were all answered 3002 ("peer not connected"), Gx sessions were not bound to
  their PCEF, and the gateway sent no DWR to its peers and no DPR on shutdown. All of these now work
  for every whitelisted peer.

### Known limitations

- **SGd does not work** in this or any earlier release. Its commands are defined under the S6c
  Application-Id (16777312; SGd is 16777313, TS 29.338 §4.8), without the AVP rules go-diameter
  needs to decode them, and several of its AVP codes are wrong; an SGd message closes the
  connection it arrives on. It will be rewritten against TS 29.338 in a later release; until then,
  do not route SGd or S6c traffic to the gateway.

### Changed

- **`server.idle_peer_timeout` is off by default** (was `5m`, never applied because no peer was
  registered). Set, it disconnects a peer that has sent no request within the window, including a
  healthy one that is only quiet. Dead peers are found by the DWR watchdog.
- **Gx and Ro envelope fields** (not yet a published contract): `pdn_connection_id` is the
  PDN-Connection-ID octets (TS 29.212 §5.3.58), base64 in JSON, instead of a number;
  `ims_information.event_type` is an object `{sip_method, event, expires}` (TS 32.299 §7.2.65)
  instead of a number. No CCR carrying either AVP was answered before this release.

## [0.10.2] — 2026-10-17

Chart 0.2.2. The gateway itself is unchanged from 0.10.1: the same binary, configuration and NATS
contract. Values written for chart 0.2.1 that set `config.nats.url` keep working unchanged.

### Fixed

- The chart no longer ships a NATS address. Its default `config.nats.url` named a namespace that
  does not exist (`nats.infra.svc.cluster.local`), so an install with the defaults never connected
  and its pods restarted in a loop. `config.nats.url` is now empty by default:
  - set to the NATS your applications use — the normal case — the gateway connects to it;
  - left empty, the chart deploys its own single-replica NATS (`nats.deploy`, core NATS, no
    storage) so a trial install starts and becomes Ready. No application is connected to that NATS,
    and the install notes say so. It has no authentication; a NetworkPolicy admits only the
    release's gateway pods to it;
  - left empty with `nats.deploy=false`, the chart refuses to render.

  The SHACL shapes reject an empty `natsUrl` when `appEnv` is production-like.

## [0.10.1] — 2026-10-17

No change to the gateway's behaviour, configuration or NATS contract.

### Licensing and packaging

- The image runs on Alpine 3.24 (was 3.18, out of support since May 2025) and builds the admin UI
  with Node.js 24 LTS (was 18, out of support since April 2025). The image's contents are otherwise
  unchanged: the same binary, configuration, admin UI files and licence texts.
- Chart 0.2.1 deploys 0.10.1; its values and schema are those of 0.2.0.
- CI builds the image on every pull request and starts it next to a NATS server
  (`scripts/image-smoke.sh`: health, admin UI, non-root user, licence texts). `make image-smoke` runs
  the same check locally.

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

[Unreleased]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.10.3...HEAD
[0.10.3]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.10.2...v0.10.3
[0.10.2]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.10.1...v0.10.2
[0.10.1]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.10.0...v0.10.1
[0.10.0]: https://github.com/3eAI-Labs/diameter-ha-gateway/releases/tag/v0.10.0
