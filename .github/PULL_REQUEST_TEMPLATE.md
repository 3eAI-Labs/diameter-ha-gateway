<!--
Development happens in a private repository and this one is published as a snapshot per release,
so your commit will not appear verbatim in this repository's history. The change itself is applied
upstream and credited in CHANGELOG.md. See CONTRIBUTING.md.
-->

## What this changes

<!-- And why. Link the issue it addresses. -->

## Wire contract

The NATS subjects and message schema are owned by diameter-ha-gateway-client.

- [ ] Not applicable — this does not change what goes over NATS
- [ ] The matching client change is linked here

## Checks

- [ ] `go vet ./...` and `go test -race ./...` pass
- [ ] Protocol changes come with a test on the exact octets, written from the specification
      (not from the encoder's own output), citing the clause
- [ ] Tests added or updated for behaviour changes, and they fail without the change
- [ ] `CHANGELOG.md` updated under `## [Unreleased]` if this is user-visible
- [ ] No real subscriber identities (IMSI, MSISDN) or operator Diameter identities in tests, logs or captures
