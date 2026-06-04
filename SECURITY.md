# Security Policy

Agent Observatory is a local-first tool that inspects coding-agent context and
selected outbound LLM-provider requests. This project deliberately uses local TLS
interception for the verified-evidence tier, so the security boundary is part of
the product surface, not an implementation detail.

## Supported Versions

The public release line is currently pre-1.0. Security fixes are expected to land
on the latest published release only.

## What Observatory Captures

Agent Observatory has two evidence modes:

- **Observed** evidence reads local agent transcripts from disk.
- **Verified** evidence inspects allowlisted outbound provider requests through a
  local NetworkExtension transparent proxy and loopback TLS-terminating proxy.

Only allowlisted LLM-provider TCP `:443` flows are routed to the local proxy.
Non-provider flows are direct-relayed after SNI inspection and are not
TLS-terminated by Observatory.

## Local CA And Trust

Verified capture uses a local CA so agent runtimes accept the loopback proxy's
leaf certificates for inspected provider hosts.

- The CA is stored under the user's local state directory.
- The private key is local to the user account.
- Trust is added to the user's login keychain, never the System keychain.
- Runtime trust env vars are additive (`NODE_EXTRA_CA_CERTS`,
  `CODEX_CA_CERTIFICATE`) and do not replace system roots.
- Transparent full capture is gated by source identity and current runtime trust;
  unsupported or stale-trust provider flows are tunneled without TLS termination.
- `agents uninstall` removes the daemon, state directory, runtime env block, and
  Observatory CA trust entries.

Any process running as the same macOS user while capture is installed may be able
to read the local CA private key. Treat enabled capture as a same-user local
trust boundary.

## Prompt Data Handling

The proxy parses request bodies in memory to derive facts such as endpoint,
prompt length, instruction matches, and tool names.

By default, raw prompt bodies are not persisted to disk. On disk, persisted wire
artifacts are redacted derived facts. In memory, the daemon keeps a bounded ring
of recent captures to drive the live feed and verified fact matching.

## Known Security-Relevant Limitations

- Already-running agents may not inherit the additive trust env. Source-aware
  policy tunnels stale-trust provider flows; a client TLS trust failure still
  pauses future full-capture flows so provider traffic passes through.
- HTTP/3/QUIC is not captured.
- ECH/no-SNI flows fail open and are not captured.
- Inspected provider requests are replayed upstream over HTTP/1.1.
- Session-to-capture correlation is coarse in the current release.

## Reporting A Vulnerability

Do not open a public GitHub issue for a vulnerability that could expose local
prompt data, weaken uninstall/trust cleanup, or expand capture beyond the
documented provider allowlist.

Report privately to the repository owner. Include:

- affected version or commit;
- macOS version;
- exact install/capture state (`agents status`, extension state, relevant logs);
- minimal reproduction steps;
- whether prompt data, local files, CA private key material, or unrelated traffic
  could be exposed.

The expected response is acknowledgement, triage, and either a fix or a documented
decision if the report describes an accepted local-trust tradeoff.
