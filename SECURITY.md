# Security

## Threat model — read this before deploying

Crew Chief is a gateway that **holds credentials and spends money**. Three
properties matter more than anything else in this file:

1. **There is no authentication.** None. Any request that reaches the port can
   delegate work.
2. **Provider API keys live in the process environment.** Cloudflare, Bedrock,
   Anthropic — whatever your presets reference. Crew Chief reads them at call
   time and sends them upstream.
3. **Therefore, anyone who can reach the port can spend your money.** Not
   "read your data" — *spend*, against real billing, with no rate limit and no
   audit trail beyond the ledger.

`serve` binds `127.0.0.1:8181` by default for exactly this reason. Exposing it
requires an explicit `--addr`, and doing so logs a warning naming this risk.

**Serving other hosts is a legitimate thing to do** — a laptop talking to a
fleet box on a trusted LAN or a Tailscale network is the design's main use
case. Putting it on a public interface is not. There is nothing between an
open port and your provider bill.

Other properties worth knowing:

- **Prompts and responses are archived in full**, content-addressed on local
  disk, and the ledger indexes them. If your tasks contain sensitive material,
  that material is at rest on the gateway box, unencrypted. There is no
  retention policy yet.
- **Delegated content is sent to whatever provider a preset names.** Routing a
  task to a cloud preset means sending it to that vendor under their terms.
- **The gateway does not execute model output.** This is deliberate. An earlier
  version ran generated code to grade it, which meant unsandboxed execution on
  the gateway box; that entire path was removed. Crew Chief only relays text.
- **`/health` is unauthenticated** and probes every configured preset when
  called. Harmless on loopback, a cheap amplification vector if exposed.

## Reporting a vulnerability

**Do not open a public issue.**

Report privately through GitHub's
[private vulnerability reporting](https://github.com/ChadDahlgren/chaio-crewchief/security/advisories/new)
— the "Report a vulnerability" button under the Security tab. That opens a
channel visible only to the maintainer.

If that isn't available to you, email **me@chad-dahlgren.com** with
`chaio-crewchief security` in the subject.

Useful to include: what an attacker can do, the version (`chaio-crewchief
version`), and a reproduction if you have one. A short description of impact
beats a long description of mechanism.

### What to expect

This is a single-maintainer project, not a vendor with an on-call rotation. Be
realistic about the response you'll get:

- **Acknowledgement within a week.** If you hear nothing in two, assume the
  message was missed and email directly.
- Assessment and a fix timeline after that, communicated in the same thread.
- Credit in the advisory and release notes, unless you'd rather not be named.

There is no bug bounty.

### Scope

**In scope:** the gateway (`gateway/`), the release and installation path
(GoReleaser config, `scripts/install.sh`, the published Homebrew cask), and the
Claude Code plugin.

**Out of scope**, though still worth telling me about:

- The absence of authentication. It's documented above, it's the known design,
  and `--addr` exposure is the operator's decision. A *bypass* of the loopback
  default would be in scope.
- Vulnerabilities in models or providers Crew Chief talks to.
- Anything requiring an attacker who already has shell access on the gateway
  box — at that point the API keys are readable directly.

## Supported versions

Pre-1.0: only the latest release gets fixes. There are no maintained release
branches, and no backports.
