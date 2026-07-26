<!--
PRs are squash-merged, so this title and body become the commit message on
main. Write them like you're writing that commit: what changed, and why the
previous behavior was wrong.
-->

## What this changes

## Why

<!-- What was wrong before? The diff already shows the what. -->

## Verification

<!--
How do you know it works? Test names, commands run, output. "Tests pass" is
weaker than naming the test that fails without this change.
-->

---

- [ ] Tests cover the change (or the PR explains why they can't)
- [ ] `go test ./...` and `go vet ./...` pass in `gateway/`
- [ ] Does not add output judging or model selection to the gateway — see
      [CONTRIBUTING.md](../blob/main/CONTRIBUTING.md)
