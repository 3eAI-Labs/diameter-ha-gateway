# Security Policy

## Supported versions

Only the most recent release receives fixes. There are no long-term support branches.

## Reporting a vulnerability

**Do not open a public issue.**

Use GitHub's private vulnerability reporting:
[**Report a vulnerability**](https://github.com/3eAI-Labs/diameter-ha-gateway/security/advisories/new).
The report stays private between you and the maintainers until a fix is published.

Useful things to include, roughly in order of how much they help:

- Version (container image tag or commit) and the affected component (Diameter base protocol,
  S6a, S13, Gx, Gy/Ro, SGd, NATS bridge, admin API, Helm chart)
- What an attacker gains — the impact matters more than the mechanism
- A reproduction: a failing test, a PCAP, or the exact message sequence. Strip real subscriber
  identities (IMSI, MSISDN), authentication vectors and operator Diameter identities before
  attaching captures
- Transport (SCTP, TCP, TLS) and the relevant configuration, credentials removed

You will get an acknowledgement, and an honest estimate rather than a promised deadline. If a
report turns out to be a plain bug rather than a vulnerability, you will be told so and asked to
move it to a public issue.

## Scope

In scope — anything in this repository that lets an attacker:

- get the gateway to accept a peer, or a request from a peer, that its configuration should
  refuse: Origin-Host/Origin-Realm spoofing, capabilities exchange bypass, requests before CER/CEA
- route a request or an answer to the wrong peer or the wrong NATS caller (Session-Id,
  Hop-by-Hop or End-to-End identifier confusion)
- crash the gateway or exhaust its state (peer table, pending-request maps, session registries)
  with malformed or crafted Diameter input
- read or alter subscriber data (IMSI, MSISDN, location, authentication vectors) that should not
  reach them, including through logs, metrics or NATS subjects
- obtain secrets from a deployment that followed the Helm chart's documented production settings

Out of scope:

- weaknesses of Diameter itself that the standards leave to network operators — the gateway is
  not a substitute for a Diameter edge agent or signalling firewall at the network edge
- the NATS server, the Kubernetes cluster, or a deployment's own configuration — for example NATS
  without TLS or authentication, or a Diameter port exposed to untrusted networks
- the admin API (`/api/admin/v1`) while `admin.auth.enabled` is false, as it is by default outside
  production: it then has no authentication, so keep it on a network only operators can reach
- the smoke/development defaults of the Helm chart, which the chart documents as unsuitable for
  production
