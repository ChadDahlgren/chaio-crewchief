# Contributing

Thanks for looking. This is a small, single-maintainer project — read the
design boundary below before writing code, because it's the thing most likely
to make an otherwise good pull request unmergeable.

## The design boundary

**Crew Chief never judges output.** It relays a work order to a model, retries
only *mechanical* failures, and returns whatever came back. `status` is
`delivered` or `failed` — never `verified`, `solved`, or scored.

This is the most common thing contributors try to add back, usually with good
intentions: a quality score, a "did it compile" check, a retry when the answer
looks wrong. **Those will be declined.** Two reasons, both load-bearing:

1. Running model-generated code to grade it means unsandboxed execution on the
   gateway box. That was a real hole in an earlier version, and removing it was
   the point of the rewrite.
2. Someone still has to define "correct." A gateway that decides is a gateway
   making design decisions on behalf of whoever actually understands the task.
   The calling brain has the context and the intelligence; it should decide.

**Crew Chief also never chooses a model.** `routing.yaml` is a table the
operator maintains. Inferring "the best model for this" belongs in the caller,
not here.

If you want output graded, that's [chaio-bench](https://github.com/ChadDahlgren/chaio-bench)
— a separate, offline tool for deciding what goes in your routing table.

None of this means the boundary can never move. It means moving it is a design
conversation in an issue, not a surprise in a diff.

## Getting set up

The Go module lives in `gateway/`, not the repository root.

```bash
git clone https://github.com/ChadDahlgren/chaio-crewchief
cd chaio-crewchief/gateway

go build -o chaio-crewchief ./cmd/chaio-crewchief
go test ./...
go vet ./...
```

Optional, matching CI:

```bash
golangci-lint run ./...                  # brew install golangci-lint
goreleaser check                         # brew install goreleaser
goreleaser release --snapshot --clean --skip=publish
```

Run it against a real model by pointing a preset at any OpenAI-compatible
endpoint — see [`examples/`](examples/) for llama.cpp, Cloudflare Workers AI,
Bedrock, and Anthropic recipes.

## Making a change

1. Branch off `main`. `main` is protected; everything lands through a pull
   request with green CI, including the maintainer's own work.
2. **Write a test.** Every bug fixed in this repository so far has shipped with
   a test that fails without the fix. If a test can't express it, say why in
   the PR.
3. Keep the commit message explanatory. Say what changed and *why the previous
   behavior was wrong* — the diff already shows the what.
4. Open the PR. CI runs vet, race tests, lint, a build matrix, an end-to-end
   smoke, plugin manifest validation, and a release-config check.
5. PRs are squash-merged, so your PR title and body become the commit message
   on `main`. Write them like you're writing that commit.

## What's especially welcome

- **Provider recipes.** Any OpenAI-compatible endpoint should be one YAML
  block. If you got one working and it needed a quirk, add it to `examples/`.
- **Bug reports with a reproduction.** A preset and a request body that
  misbehaves is worth more than a description of it.
- **Platform fixes.** Releases cover linux and darwin on amd64 and arm64;
  everything else is untested by definition.
- **Documentation that corrects something wrong.** Especially in `examples/` —
  a stale recipe is worse than a missing one.

## What to open an issue about first

- Anything touching the design boundary above
- New API surface, new endpoints, new response fields
- Anything adding a dependency to the gateway — it is deliberately a single
  static binary with a small dependency tree, and that's worth protecting
- Large refactors

An issue costs you five minutes. A rejected 800-line PR costs you an evening.

## Style

Standard Go: `gofmt` (CI enforces it via lint), standard library first, and
comments that explain *why* rather than restate the code. The existing
comments are the reference — several of them exist specifically to stop a
future reader from "fixing" something deliberate.

## Security

Don't open a public issue for a vulnerability. See [SECURITY.md](SECURITY.md).

## License

Contributions are accepted under [Apache-2.0](LICENSE), the project's license.
