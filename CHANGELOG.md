# Changelog

All notable changes to diameter-ha-gateway are documented in this file.
Format loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[SemVer](https://semver.org/) (pre-1.0: a minor version may be breaking).

Versions up to 0.9.4 were released privately; 0.10.0 is the first public release. Its entry
describes what changed for an installation of 0.9.4.

## [Unreleased]

## [0.14.0] — 2026-10-18

Chart 0.3.3 (values and schema unchanged). Gx, Gy and Ro release: Gx follows TS 29.212 Table
5.3.0.1, and Gy and Ro share one Credit-Control codec that follows TS 32.299 §7.1.9. Applications
using Gx, Gy or Ro: read *Changed* — several enums are now pointers, so a field set to 0 is sent and
a field left out is not.

### Fixed

- **Gx follows TS 29.212** (P3-15 of the conformance audit):
  - Every 3GPP AVP the gateway writes takes its M bit from Table 5.3.0.1; Default-EPS-Bearer-QoS,
    Usage-Monitoring-Information and its children, Monitoring-Key, Allocation-Retention-Priority,
    Priority-Level and Pre-emption-Capability/-Vulnerability carried an M bit the table forbids.
  - Every AVP of the table is in the Gx dictionary with its type: the PCEF's Charging-Rule-Report
    (CCR and RAA) reached the application empty, and ToS-Traffic-Class decoded as Unsigned32.
    Reading a CCR no longer assumes an AVP's type.
  - The RAA's Charging-Rule-Report reaches the application; the reply carried the result only.
  - APN-AMBR is QoS-Information's APN-Aggregate-Max-Bitrate-UL/DL; the gateway wrote and read
    AVP 1435, which is S6a's. `apn_ambr` still works, folded into QoS-Information.
  - Flow-Description is sent inside Flow-Information, with the Flow-Direction §5.3.53 requires
    (from the description's "in"/"out" when not given); `flow_information` and
    `redirect_information` of a Charging-Rule-Definition are sent; they were ignored.
  - Online and Offline are sent only when given; they were always sent, as 0 when absent.
  - A CCR's second AN-GW-Address reaches the application (`an_gw_addresses`).
  - A CCA can carry an Experimental-Result (`experimental_result_code`, e.g. 5140) without
    Result-Code, and a Session-Release-Cause (§4.5.9).
  - `IPCANType3GPP_5GS` is 8 (3GPP-5GS); 9 is Non-3GPP-5GS. `EventTriggerRATTypeChange` is
    `EventTriggerRAIChange` (RAI_CHANGE, 12); `EventTriggerDefaultEPSBearerQoSMod` is
    `EventTriggerDefaultEPSBearerQoSModificationFailure` (34).

- **Gy and Ro share one Credit-Control codec** (P3-16), so Ro sends what Gy sends. In the CCR,
  every Service-Identifier of an MSCC, the Used-Service-Unit's Reporting-Reason and
  Tariff-Change-Usage, the Trigger, Envelope, Refund-Information and the other MSCC fields of TS
  32.299 §7.1.9 reach the application; in the CCA, the MSCC carries every field the application
  gives — Ro wrote only Rating-Group, Service-Identifier, Granted-Service-Unit, Result-Code,
  Validity-Time and a Final-Unit-Action without its Redirect-Server.
  - Rating-Group is optional and is no longer sent as 0 when the application gives none.
  - Quota-Holding-Time 0 (the mechanism disabled, §7.2.160) and an empty Trigger (every trigger
    disarmed, §7.2.235) can be sent.
  - A CCA carries one Failed-AVP holding every failed AVP, with its flags; it carried one per AVP,
    without flags.
  - CC-Session-Failover, Cost-Information and Remaining-Balance are sent, and Low-Balance-Indication
    NOT-APPLICABLE (0); Gy's Redirect-Host is sent. All were accepted and dropped.
  - Reading a CCR no longer assumes an AVP's type; an empty Requested-Service-Unit reads as
    present.
  - PS-Furnish-Charging-Information is sent only with its mandatory 3GPP-Charging-Id (V bit
    only, TS 29.061), and PS-Append-Free-Format-Data only when given: absent means 'Overwrite',
    0 'Append' (§7.2.155).
- README: no OCS-initiated Gy or Ro request (RAR, ASR) is implemented.

### Changed

- **Gx envelopes** (not yet a published contract): enums whose 0 is a value are pointers, so 0 can
  be sent and absent is told apart — in the CCR event `ip_can_type`, `rat_type`,
  `bearer_operation`, `an_gw_status`, `network_request_support`; in the PCRF's replies and requests
  `flow_status`, `reporting_level`, `online`, `offline`, `metering_method`,
  `usage_monitoring_level`/`_report`/`_support`, `pre_emption_capability`/`_vulnerability`,
  `flow_direction`, `redirect_support`, `redirect_address_type`, `session_release_cause`; and
  `pcc_rule_status`. In JSON only absence changes meaning: a field set to 0 is now sent, and a
  field left out is not, so the specification's default applies (Pre-emption-Capability DISABLED,
  Redirect-Support ENABLED).
- **Gy and Ro envelopes** (not yet a published contract): in an MSCC `rating_group`,
  `quota_holding_time`, `envelope_reporting` and `tariff_change_usage` are pointers, and the
  service units' `tariff_change_usage` and `reporting_reason`; `service_identifiers` lists every
  Service-Identifier (`service_identifier` keeps the first); the CCA's `cc_session_failover` and
  `low_balance_indication` are pointers, and `ps_append_free_format_data`; `cost_information` is new.

## [0.13.0] — 2026-10-18

Chart 0.3.2 (values and schema unchanged). S6a release, with client module 0.2.0: the rest of
each S6a message reaches the application and can be sent by it, and four behaviours that broke
TS 29.272 or TS 29.229 are fixed. Applications that set `mip6_home_link_prefix`: it is now hex.

### Fixed

- **S6a:**
  - Supported-Features in ULA, AIA, PUA and NOA no longer carries the M bit ("within an answer
    application message the AVP shall never have the 'M' bit set", TS 29.229 §7.2.1), nor do
    Feature-List-ID and Feature-List anywhere (Table 6.3.1).
  - ULA-Flags and PUA-Flags are sent only with DIAMETER_SUCCESS (TS 29.272 Tables 5.2.1.1.1/2,
    5.2.1.3.1/2); a failed answer carried them too.
  - A CLR carries the application's `cancellation_type`; it was always 0 (MME_UPDATE_PROCEDURE).
  - An IDR without subscription data, which only requests user state, location, reachability or
    time zone, carries the mandatory Subscription-Data empty (§5.2.2.1.3); it was left out.
  - A NOR's `service_selection` took the Service-Selection of a ULR's first Active-APN; it is read
    at top level only.

### Added

- **S6a: the rest of each message reaches the application** (client module 0.2.0):
  - from the MME's ULR, AIR, PUR and NOR: Active-APN whole (with its Context-Identifier),
    SMS-Register-Request, SGs-MME-Identity, Coupled-Node-Diameter-ID, Adjacent-PLMNs, the
    Terminal-Information's 3GPP2-MEID, AIR-Flags, the PUR's EPS-Location-Information, the NOR's
    Maximum-UE-Availability-Time and Visited-Network-Identifier;
  - from the MME's CLA, IDA, DSA and RSA, which reached the application as their result code
    only: Supported-Features, Failed-AVP, DSA-Flags and the IDA's IDA-Flags,
    IMS-Voice-Over-PS-Sessions-Supported, Last-UE-Activity-Time, RAT-Type, EPS-User-State,
    EPS-Location-Information and Local-Time-Zone.
- **S6a: what an application can send.** CLR-Flags (CLR), Reset-ID (IDR, RSR), further User-Ids
  (RSR), Trace-Reference, TS-Code and SS-Code (DSR), and in all four requests Supported-Features
  and DRMP; in an AIA, UTRAN and GERAN vectors beside E-UTRAN ones, and UE-Usage-Type; in a ULA or
  AIA, Error-Diagnostic. An invalid hex value is answered 5004 and not sent.

### Changed

- **`mip6_home_link_prefix` is hex** (client module 0.2.0), the prefix length octet and the
  16-octet prefix (RFC 5447 §4.2.4), in subscription data and in Active-APN. The gateway wrote the
  string's bytes, which JSON cannot carry for a binary prefix.

## [0.12.0] — 2026-10-18

Chart 0.3.1 (values and schema unchanged). S6c and SGd release: both applications are rewritten
against TS 29.338 V17.2.0 and work for the first time. Applications using S6c or SGd: read *Changed*;
the fields earlier releases accepted on `diameter.sgd.srr.request` and `diameter.sgd.tfr.request`
still work.

### Fixed

- **S6c and SGd work** (TS 29.338 V17.2.0). They are rewritten: up to this release no S6c or SGd
  message could be decoded and each closed the connection it arrived on, and nothing the gateway
  sent was well formed.
  - OFR and TFR are under the SGd Application-Id 16777313, SRR, ALR and RDR under the S6c
    Application-Id 16777312; ALR uses 16777312 over SGd as well (§6.3.2.2). A CER offering SGd is
    accepted.
  - Every command's AVPs follow its Command Code Format, with the codes, types and M bits of TS
    29.338, 29.173, 29.272, 29.336 and 29.229; the M bit of a re-used AVP is the one each
    application gives it. Ten AVPs had wrong codes or types, among them MME-Name, MME-Realm,
    SM-Delivery-Outcome and Additional-Serving-Node.
  - Report-SM-Delivery-Status (RDR/RDA) is added, in both directions: `diameter.sgd.rdr` and
    `diameter.sgd.rdr.request`.
  - Requests carry the P flag; no message carries a Vendor-Specific-Application-Id (§5.3.2.2:
    "shall not be included"); the header's Message-Length matches the message.
  - A request missing a mandatory AVP is answered 5005 with a Failed-AVP; a 3GPP result code
    (5550-5558) is sent in Experimental-Result without Result-Code (§7.3.1), also when the
    application gives it as `result_code`.
  - An application request that would leave a mandatory AVP out (an empty `user_identifier`, a
    number with no digit, an SRR with neither `msisdn` nor `user_name`) is answered 5005 and not
    sent.
  - E.164 numbers (MSISDN, SC-Address, node numbers) are TBCD-encoded on the wire.

### Changed

- **S6c and SGd envelopes** (not yet a published contract) follow TS 29.338. For applications of
  earlier releases, the fields `diameter.sgd.srr.request` and `diameter.sgd.tfr.request` accepted
  are still accepted (`imsi`, `mme_name` and `mme_realm` as the TFR destination, `sm_rp_pri`), and
  the reply to `diameter.sgd.srr.request` still carries `mme_name`, `mme_realm`, `sgsn_name`,
  `sgsn_realm` and `user_name` at top level. Otherwise:
  - every E.164 number is a string of digits, among them `msisdn` in events and the node numbers,
    which were base64 bytes;
  - `serving_node` and `additional_serving_node` are objects, not arrays; the SRA's top-level
    `3gpp_smsc_address`, `msc_number` and `mme_number_for_mt_sms` are gone (the numbers are in
    `serving_node`);
  - SRR events carry no `user_identifier`, which SRR does not have, and no `sm_rp_pri` (SRR-Flags
    bit 1); OFR events lose `sm_rp_oa_type`, `sm_rp_oa`, `sms_gmsc_address` and
    `external_identifier`, which were never filled;
  - TFR events carry `user_name` and no `user_identifier`, `sm_rp_mti`, `sm_rp_smea`, MME or SGSN
    names, which TFR does not have; `max_retransmissions` is `maximum_retransmission_time`, a time;
  - `supported_features` is a list of `{vendor_id, feature_list_id, feature_list}`;
  - SM-Delivery-Outcome is `{mme, msc, sgsn, ip_sm_gw, smsf_3gpp, smsf_non_3gpp}`, each
    `{sm_delivery_cause, absent_user_diagnostic_sm}`;
  - the experimental result code constants are those of TS 29.338 §7 (5550-5558).

## [0.11.0] — 2026-10-18

Chart 0.3.0. Conformance release: Gx, Gy and Ro CCRs that follow the specifications are answered,
and answers, CER handling and the requests the gateway sends follow RFC 6733. Before upgrading:

- If you set `charging.gy_context_ids` or `ro_context_ids`, check that they hold the
  Service-Context-Ids your network sends (3GPP: `32251@3gpp.org` for Gy, `32260@3gpp.org` and
  `32275@3gpp.org` for Ro). If you left them at the shipped `ocs.diameter@3gpp.org` and
  `ims-charging@3gpp.org`, remove them to take the new defaults, unless your network really sends
  those values.
- `charging.gx_context_ids` no longer has any effect.

### Fixed

- **Gx, Gy and Ro CCRs that follow the specifications are answered.** A Gx CCR carries no
  Service-Context-Id (TS 29.212), and the gateway answered every one without it 5012; the gateway
  now recognises a Gx CCR by its Application-Id (16777238). The default Gy and Ro
  Service-Context-Ids, `ocs.diameter@3gpp.org` and `ims-charging@3gpp.org`, are not 3GPP values,
  so every CCR carrying the 3GPP values was answered 5012. The defaults are now `32251@3gpp.org`
  (Gy, PS charging) and `32260@3gpp.org` and `32275@3gpp.org` (Ro, IMS and MMTel charging), and an
  entry also matches a Service-Context-Id with a release or operator prefix, such as
  `17.32251@3gpp.org` (TS 32.299 §7.1.12).
- **A CCR missing a mandatory AVP is answered 5005** (RFC 8506 §3.1) with a Failed-AVP. A Gy or Ro
  CCR without CC-Request-Type was answered 2001; one without Service-Context-Id 5012.
- **Requests the gateway sends carry the P flag.** CLR, IDR, DSR, RSR and Gx RAR and ASR are
  proxiable (`REQ, PXY`); a Diameter agent between the gateway and the peer may refuse to relay a
  request without the flag. SGd requests still lack it (SGd does not work; see Known limitations
  in 0.10.3).
- **Answers follow RFC 6733:**
  - every Proxy-Info of the request is copied into the answer, in order (§6.2);
  - a 3xxx answer carries the E flag (§7.1.3), and the answers the gateway builds no longer carry
    the T flag (§3), which they copied from a retransmitted request; the CEA and DWA go-diameter
    builds still copy it;
  - an application reply without a result code is answered 5012 instead of producing an answer
    with neither Result-Code nor Experimental-Result;
  - a Failed-AVP for a missing AVP carries a zero value of the AVP's minimum length (§7.5), which a
    peer can decode; it carried an empty value, which go-diameter itself could not decode;
  - a request for a command the gateway does not serve is answered 3001 (COMMAND_UNSUPPORTED), one
    for a base command under an application the gateway does not know 3007
    (APPLICATION_UNSUPPORTED); both were left unanswered. A request whose command go-diameter does
    not know for its Application-Id still fails to decode, and go-diameter closes the connection.
- **CER checks.** A CER without Host-IP-Address, Vendor-Id or Product-Name is answered 5005; one
  whose Vendor-Specific-Application-Id holds both an Auth- and an Acct-Application-Id 5009, and one
  whose Vendor-Specific-Application-Id holds neither 5005 (RFC 6733 §5.3.1, §6.11). Each answer
  carries a Failed-AVP, and the connection is closed. All were accepted with 2001.
- **An error answer without a Session-Id reaches the application.** The answer to a request the
  gateway sent is matched by its hop-by-hop identifier when it has no Session-Id (RFC 6733 §7.2);
  it was dropped, and the application waited for `reverse_timeout`.
- **A redirected request carries no T flag.** It follows an error answer (3006), after which RFC
  6733 §3 forbids the flag. The `diameter_tbit_retransmit_total` metric is removed.

### Changed

- **Session-Ids of gateway-initiated requests** take the RFC 6733 §8.8 form
  `<origin-host>;<high 32 bits>;<low 32 bits>;<kind>`. They began with the subscriber's IMSI, which
  every log line carrying the Session-Id then revealed despite IMSI masking.
- **`charging.gx_context_ids` is ignored** and logged at start-up when set; the key still loads
  and validates.

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

[Unreleased]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.14.0...HEAD
[0.14.0]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.10.3...v0.11.0
[0.10.3]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.10.2...v0.10.3
[0.10.2]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.10.1...v0.10.2
[0.10.1]: https://github.com/3eAI-Labs/diameter-ha-gateway/compare/v0.10.0...v0.10.1
[0.10.0]: https://github.com/3eAI-Labs/diameter-ha-gateway/releases/tag/v0.10.0
