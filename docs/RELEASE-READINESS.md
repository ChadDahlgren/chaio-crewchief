# Chaio Crew Chief — open source readiness runbook

One-time checklist to take Chaio Crew Chief from a private working deployment
to a properly managed public project. Written 2026-07-25.

**Naming, as settled in Phase 0:** the brand is **Chaio**, the product is
**Crew Chief**, and everything shipped is `chaio-crewchief` — repo, module,
binary, Homebrew formula, Claude Code plugin. MCP *tool* names stay
`crewchief_*`, because some MCP clients don't namespace tools by server and
bare `delegate`/`stats` would collide. The `chaio-` prefix is a deliberate
brand play: it disambiguates from two existing CrewChiefs and gives future
tools a shared front door.

**Order matters.** Phases 1–4 happen while the repo is still private. Going
public (Phase 6) is the irreversible step and comes last, after everything
else is verified. Nothing below is urgent; each phase stands alone and the
repo is usable at every point in between.

**Ground rule adopted for this project:** `main` is protected. All work lands
through a pull request with green CI. No direct pushes, no auto-merge.

---

## Phase 0 — Decide the identity (do first; blocks the rename cost)

Everything downstream — the repo URL, the Homebrew formula, the binary name,
the MCP server name — bakes in the project name. Changing it after publishing
is expensive and confusing. This project has already been renamed twice
(Dispatch → Foreman → Crew Chief); make this the last time.

- [x] **Name availability checked** (2026-07-25) with:
  ```bash
  gh search repos <name> --limit 20             # GitHub collisions
  npm view <name> version                        # npm squatters
  gh api users/<name>                            # org name free?
  ```
  **Findings that forced the rename:** `manifoldlogic/crewchief` publishes
  `@crewchief/cli` v1.7.0 — *"Multi-agent orchestration CLI for coordinating
  AI agents via git worktrees"* — the same name doing adjacent work, actively
  shipping. Separately, `CrewChiefV4` (120★) is the dominant "CrewChief" in
  software, a sim-racing spotter with its own ecosystem. Homebrew was clear,
  and a personal tap has no shared namespace, so the conflict was
  discoverability and confusion rather than mechanics.

  Also ruled out on evidence: `foreman` (`ddollar/foreman`, 6157★, **and**
  occupies `foreman` in Homebrew core), `dispatcher` (npm taken, 11k★ top
  hit, too generic), `understudy` (`understudy-ai/understudy`, 451★ — AI
  space), `domestique` (`ebowman/domestique` is a Claude Code installer).
- [x] **Renamed to `chaio-crewchief`.** Repo, Go module, binary, `cmd/`
      directory (which is what `go install` derives the binary name from),
      systemd service name, DB filename, plugin, MCP server name, and the
      `CHAIO_CREWCHIEF_URL` env var — with `CREWCHIEF_URL` and `DISPATCH_URL`
      kept as fallbacks so pre-rename configs keep working.
- [ ] **Rename the GitHub repo** from `crewchief` to `chaio-crewchief`, and
      rename the local working directory to match.
- [ ] Update the GitHub repo description — it still reads **"Claude Foreman"**
      (two renames ago). Suggested: *"A deliberately simple, fast, cheap phonebook between
      your frontier model and a fleet of cheap ones — with a cost ledger."*
- [ ] Add repo topics for discoverability: `llm`, `local-llm`, `mcp`,
      `claude-code`, `go`, `cloudflare-workers-ai`, `llama-cpp`.
- [ ] Confirm the license. `LICENSE` is Apache-2.0 — a good default here
      (permissive plus an explicit patent grant). Keep it.

---

## Phase 1 — History reset

**Why:** the current history contains the dead-end verify subsystem, a Node
MCP shim, and two prior project names. None of it is sensitive — a full-history
grep for secrets, credentials, and personal/employer identifiers came back
clean — but a first-time reader shouldn't have to parse three renames to
understand what this is.

**Cost:** you lose the build provenance (the record of a local model
implementing most files against frozen contracts). That story lives on in
`model-lab/`, so it isn't lost, just not visible here.

**Safe because:** the repo is private, unpublished, unforked, and has no
outside contributors. After Phase 6 this stops being safe — a force-push over
public history breaks every clone and fork. This is the last moment it's free.

- [ ] **Back up the old history first**, so the provenance is recoverable:
  ```bash
  cd <parent directory of your clone>
  git clone --mirror chaio-crewchief chaio-crewchief-history-backup.git
  ```
  Keep that bundle off the SSD too (it is small — ~180 KiB).
- [ ] Land the pending `fix/pre-release-hardening` branch first, so the reset
      captures the fixed state rather than needing a follow-up commit.
- [ ] Squash to a single root commit:
  ```bash
  cd chaio-crewchief
  git checkout main
  git checkout --orphan clean-start
  git add -A
  git commit -m "Crew Chief: delegation gateway for hybrid model fleets"
  git branch -M clean-start main
  ```
- [ ] Verify the new tree is byte-identical to the old one before pushing —
      this is the check that proves the reset changed history and nothing else:
  ```bash
  git diff --stat clean-start <old-main-sha>   # must be empty
  ```
- [ ] Force-push to the remote, then confirm the old commits are gone from the
      GitHub UI (including any dangling refs under Releases/Tags).
- [ ] **The local clone has no `origin` configured** — fix that first or the
      push has nowhere to go:
  ```bash
  git remote add origin git@github.com:ChadDahlgren/chaio-crewchief.git
  ```

> **Caveat on "erased forever":** force-pushing removes commits from the
> default view, but GitHub can retain unreferenced objects server-side and
> they may stay reachable by SHA for a period. Since nothing sensitive is in
> the history, that's acceptable here. If that ever changes, the only reliable
> remedy is deleting the repository and creating it fresh — which is also an
> option now, and is the strongest form of this reset.

---

## Phase 2 — The files an open source project is expected to have

A project without these reads as a code dump. With them it reads as
maintained, which is most of what makes a stranger willing to depend on it.

- [ ] **`README.md`** — already strong. It leads with what the thing is, and
      the "Why not have Crew Chief verify the work too?" section is genuinely
      good writing: it explains a design decision by explaining what was tried
      and rejected. Keep the honest Status section. Add: a one-line install
      (after Phase 4), a screenshot or paste of `chaio-crewchief usage` output (the
      cost ledger is the hook — show it), and a short "is this for me?"
      qualifier so people self-select out fast.
- [ ] **`CONTRIBUTING.md`** — how to build, how to run tests, that PRs need
      green CI, and what you will and won't accept. Be explicit that the
      no-judging design rule is not up for negotiation: the most common PR
      you'll get is someone helpfully re-adding a quality score.
- [ ] **`SECURITY.md`** — how to report a vulnerability privately (enable
      GitHub Private Vulnerability Reporting), and a plain statement of the
      threat model: *no authentication, binds loopback by default, holds
      provider API keys in its environment, intended for trusted networks.*
      Say this loudly. It is the single most likely way a user gets hurt.
- [ ] **`CODE_OF_CONDUCT.md`** — Contributor Covenant, unmodified. Takes two
      minutes and heads off a category of problem you don't want to improvise
      a policy for later.
- [ ] **`CHANGELOG.md`** — Keep a Changelog format. Start at `v0.4.0` and
      write the entry for it as part of the release, not after.
- [ ] **Issue + PR templates** (`.github/ISSUE_TEMPLATE/`) — a bug template
      that asks for `chaio-crewchief version`, `chaio-crewchief doctor` output, and the
      relevant `models.yaml` preset **with keys redacted**. That last
      instruction prevents users pasting live credentials into public issues.
- [ ] **`examples/`** already covers llama.cpp, Cloudflare, Bedrock, and
      Anthropic. Verify each one still works against the current binary before
      launch — stale examples are the fastest way to lose a new user.
- [ ] **`chaio-bench` needs its own pass** before it goes public: it inherits
      none of this repo's CI, and it still carries an unpatched
      `@angular/core` advisory (no fix on the 19 line; npm's remedy is a jump
      to 22, which would invalidate comparability with results already
      recorded). Decide there whether to bump the Angular track or document
      the exposure — it's a local, offline test harness with no server and no
      untrusted input, so the advisory's attack surface isn't reachable.

---

## Phase 3 — CI and branch protection

**Your CI is already good** — better than most projects at this stage. It runs
vet, race tests, golangci-lint, a 3-platform build matrix, an end-to-end smoke
(serve → sync delegate → `retries:0` → async poll → doctor → usage → MCP stdio
handshake), and plugin manifest validation. Don't rebuild it. Add the
governance layer around it.

- [ ] **Protect `main`** (Settings → Rules → Rulesets, or Branch protection):
  - Require a pull request before merging
  - Require status checks to pass: `test`, `lint`, `build-matrix`, `smoke`,
    `plugin-validate`
  - Require branches to be up to date before merging
  - Block force pushes and deletions
  - **Do not enable auto-merge**
  - Apply to administrators (include yourself — the point is the discipline)
  > Branch protection on **private** repos requires a paid plan. On **public**
  > repos it's free. If you're on the free tier, this becomes available the
  > moment you flip to public in Phase 6 — sequence accordingly.
- [ ] **Pin CI actions to commit SHAs**, not floating tags. `actions/checkout@v4`
      is mutable; a compromised tag runs arbitrary code with your repo token.
      This matters more once the repo is public.
- [ ] **Add Dependabot** (`.github/dependabot.yml`) for `gomod` and
      `github-actions`, weekly. Low noise for a project with this few deps.
- [ ] **Add CodeQL** scanning (free for public repos). One workflow file.
- [ ] **Add a `govulncheck` step** to CI — Go's own vulnerability scanner,
      more precise than generic dependency alerts.
- [ ] Confirm `permissions:` is least-privilege in every workflow. `ci.yml`
      already sets `contents: read`; `release.yml` needs `contents: write` and
      has it. Good — just don't let it drift.
- [ ] Consider requiring signed commits once you're the only committer; it's
      cheap to adopt now and painful to retrofit.
- [ ] **Merge queue — revisit later, not now.** It solves concurrent merges
      racing each other: two PRs each pass against `main`, both merge, and the
      combination breaks. With one maintainer that doesn't happen, and the
      ruleset's "branch must be up to date" already covers the same class at
      this volume. A queue would add latency to every merge and extra CI runs
      for no benefit. Adopt it when two or more people are merging on the same
      day — and turn *off* strict up-to-date checks at that point, since the
      queue does that job instead.

---

## Phase 4 — Packaging: "install and go"

**Goal: nobody compiles Go to use this.** The release workflow already
cross-compiles four platforms and publishes checksums — it has simply never
run, because no tag has ever been pushed. GoReleaser replaces the hand-rolled
build loop and adds the piece that actually matters: it generates and pushes
the Homebrew formula for you.

**Why Homebrew over npm:** npm distribution of a Go binary means publishing
several platform-specific packages wired together with `optionalDependencies`
and a postinstall shim. It works (esbuild does it) but it's fiddly, and it
mainly buys you `npx` — which is the wrong shape for a long-running gateway.
Your users run local GPU fleets on Macs and Linux boxes; brew and curl are
native to them. Revisit npm only if you later ship a Node-side client.

- [x] **`.goreleaser.yaml` added** at the repo root (not in `gateway/`, so
      archives can include `LICENSE` and `README.md`; `builds[].dir: gateway`
      points at the module). Builds linux+darwin × amd64+arm64, `CGO_ENABLED=0`
      (safe — the SQLite driver is `modernc.org/sqlite`, pure Go), `-trimpath`,
      version injected from the tag, archives carrying the three example YAML
      configs, and `checksums.txt`.
- [x] **`release.yml` rewritten** to run `goreleaser release --clean`, keeping
      the "test first" gate so a tag pushed at a commit CI never saw still
      can't publish an untested binary.
- [x] **`scripts/install.sh` added** for Linux and Homebrew-less macOS:
      detects OS/arch, resolves the latest tag, **verifies the SHA-256 against
      `checksums.txt`**, and installs to `BINDIR` (default `/usr/local/bin`).
      Verified end to end against a local fake release — happy path installs,
      and a deliberately corrupted archive is rejected on checksum mismatch.
- [x] **CI gained a `release-config` job**: `goreleaser check`, a full snapshot
      release (all platforms, archives, checksums, cask), plus `sh -n` and
      `shellcheck` over the install script. A broken release config now fails
      on a PR rather than at tag time, when the only retry is deleting and
      re-pushing the tag.
- [x] **Homebrew cask, not formula.** `brews` is deprecated in GoReleaser, and
      Homebrew now treats casks as the vehicle for pre-built binaries. The
      generated cask includes a `postflight` that strips
      `com.apple.quarantine` — without it, an unsigned downloaded binary dies
      on first run with "the developer cannot be verified". **Casks are
      macOS-only**, which is why `install.sh` exists for Linux.
- [x] **`ChadDahlgren/homebrew-tap` created** — public (installing a tap
      clones it, so a private tap can't be installed by anyone else) and named
      exactly `homebrew-tap`, since Homebrew strips that prefix to produce
      `brew install ChadDahlgren/tap/chaio-crewchief`. Has a README noting
      that casks there are generated and direct edits get overwritten.
- [x] **`HOMEBREW_TAP_TOKEN` secret added** — a fine-grained PAT scoped to the
      tap repository only, Contents: Read and write, expiring **2027-07-25**.
      Set a calendar reminder: expiry surfaces as an opaque permissions
      failure during release, with nothing pointing at the token.
- [x] **Git remote configured** over SSH, which also fixed the snapshot cask
      rendering `https://github.com///releases/...` — GoReleaser derives
      owner/repo from the remote. Regenerated and confirmed the download URLs
      now resolve to `github.com/ChadDahlgren/chaio-crewchief/releases/...`.
- [ ] **Sign and notarize the macOS binaries** eventually, so the quarantine
      workaround can be dropped. Needs an Apple Developer account; not a
      blocker for `v0.x`.
- [ ] Uninstall the stale `fleet@fleet-marketplace` v0.1.0 plugin locally and
      install `chaio-crewchief` v0.4.0 in its place, so your own machine runs what
      you ship.

---

## Phase 5 — Remaining code hardening

Not blockers for a `v0.x` release, but each one is a thing a stranger will hit.

- [ ] **Optional bearer-token auth.** Loopback-by-default (done) closes the
      careless case. A `CREWCHIEF_TOKEN` env var checked by middleware closes
      the deliberate case — someone who legitimately needs `--addr :8181` on a
      shared network. Small, additive, no design change.
- [ ] **Request size limits.** `POST /delegate` decodes an unbounded body.
      `http.MaxBytesReader` is one line and prevents a trivial memory
      exhaustion.
- [ ] **Archive growth.** Every attempt writes prompt, response, and artifact
      blobs to disk forever — that's how the production DB reached 135 MB with
      16k attempts. Ship either a `chaio-crewchief prune --older-than 30d` command
      or documented retention guidance, before a user's disk fills.
- [ ] **`/health` is unauthenticated and probes every preset on each call.**
      Fine on loopback; a cheap amplification vector if exposed. Consider
      caching results for a few seconds.
- [ ] **Structured logging** (`log/slog`) instead of `log.Printf`, so
      operators can ship logs somewhere. Nice-to-have.
- [ ] Decide and document the **Go version support window** (currently
      whatever `go.mod` pins).

---

## Phase 6 — Go public (the irreversible step)

Do not do this until Phases 1–4 are done and verified.

- [ ] Final secret sweep with a real scanner, not grep — `gitleaks detect
      --no-git` over the worktree and `gitleaks detect` over history. Cheap
      insurance even though the manual audit was clean.
- [ ] Re-read `models.yaml`, `rates.yaml`, `routing.yaml`, and every
      `examples/*.md` with fresh eyes: no internal hostnames, no account IDs,
      no paths from your machine. (Audited clean as of 2026-07-25 — the
      shipped config uses `localhost:8080` and generic preset names.)
- [ ] Confirm no personal infrastructure is referenced anywhere. The only
      personal data in the repo is your name and email in the plugin author
      fields, which is correct and intentional for an open source project.
- [ ] Flip the repo to public.
- [ ] Enable: Private Vulnerability Reporting, secret scanning + push
      protection (free on public repos — push protection actively blocks you
      from ever committing a credential). Dependabot alerts are already on.
- [ ] **Confirm CodeQL starts running.** Its job is gated on
      `github.event.repository.visibility == 'public'` because code scanning
      needs GitHub Advanced Security on private repositories — the analysis
      succeeds and the *upload* fails with "Code scanning is not enabled for
      this repository". Going public should switch it on with no workflow
      change; verify a run actually appears rather than assuming it did.
- [ ] Apply branch protection now if the free tier blocked it earlier.

---

## Phase 7 — First release

- [ ] Land everything through PRs into protected `main`.
- [ ] Write the `CHANGELOG.md` entry for `v0.4.0`.
- [ ] Tag and push:
  ```bash
  git tag -a v0.4.0 -m "First public release"
  git push origin v0.4.0
  ```
- [ ] Watch the release workflow. **This will be its first-ever run** — expect
      to fix something. Budget time for one or two failed attempts; that's
      normal, not a sign anything is wrong.
- [ ] Verify the published artifacts on a machine that has never built this:
  ```bash
  brew install ChadDahlgren/tap/chaio-crewchief
  chaio-crewchief version     # must print v0.4.0, not "dev"
  chaio-crewchief doctor
  ```
  If it prints `dev`, the ldflags injection didn't take — fix before
  announcing.
- [ ] **Why `v0.4.0` and not `v1.0.0`:** 0.x signals the API may still move,
      which is honest and buys you room. Save 1.0 for when you're ready to
      promise stability.

---

## Phase 8 — After launch

- [ ] Decide your support posture up front and put it in the README: is this
      "I built this for me, use it if it helps" or "I intend to maintain this
      for others"? Both are fine. Saying which one prevents resentment on both
      sides.
- [ ] Expect the first issues to be install problems on platforms you don't
      have. That's what the release matrix is for.
- [ ] The most valuable thing you can publish alongside it: **your own ledger
      numbers.** "16,703 delegations, $0.53 spent, $219.87 counterfactual,
      99.8% saved" is a concrete, falsifiable claim from real production use,
      and it is far more persuasive than any feature list. Be precise that the
      bulk of that volume is a classification workload, not coding — the
      honesty is what makes the number credible.

---

## Open questions to decide, not defaults to accept

1. **Is coding delegation actually good?** 16,527 of 16,703 attempts are one
   classification workload. Coding delegation has ~176 attempts, nearly all
   benchmarks. Before promoting this as a coding tool, run it on real work and
   find out. It may be that the honest pitch is "cheap high-volume
   classification with a cost ledger," which is a *better* product story than
   a weaker coding claim.
2. ~~**Does `bench/` ship in the same repo?**~~ **Resolved 2026-07-25: split
   out to [chaio-bench](https://github.com/ChadDahlgren/chaio-bench).** The
   deciding evidence was concrete rather than aesthetic — enabling Dependabot
   immediately surfaced two high-severity advisories, both in
   `bench/web/package-lock.json`, neither reachable from the shipped Go
   binary. A single-binary Go project was carrying a JavaScript dependency
   surface it never used. The grader drives model endpoints directly and never
   touches the gateway, so nothing was actually coupled except the directory.
3. **Who is this for?** "People running a local GPU box who want their
   frontier model to delegate to it" is a small, real audience. Naming it
   plainly in the README will attract the right users and repel the wrong
   ones, which is what you want.
