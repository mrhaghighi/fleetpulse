# 0001 — Record architecture decisions

## Status

Accepted

## Context

FleetPulse is built by a small team and changes quickly. The reasons behind a
choice are usually more valuable than the choice itself, and they are lost as
soon as the pull request is merged. New contributors then reopen settled
questions, or reverse a decision without knowing what it protected.

## Decision

We record every architecture decision as a short ADR in `docs/adr`, named
`NNNN-kebab-title.md` and numbered in sequence. Each ADR has the same sections:
Status, Context, Decision, Alternatives considered, Consequences.

ADRs live in the repository next to the code they describe. Once an ADR is
accepted it is not edited. A later decision that replaces it gets a new ADR, and
the old one changes its Status to `Superseded by ADR-NNNN`. Small edits such as
fixing a broken link are allowed; changing the decision is not.

## Alternatives considered

- **Wiki pages.** Easier to write, but they drift away from the code, have no
  review step, and are invisible in a checkout.
- **Only code comments and commit messages.** Cheap, but a decision spread over
  ten commits has no single readable home.
- **A long design document.** Goes stale as one file and becomes too large to
  update for every decision.

## Consequences

Writing an ADR costs about half a page of text per decision. In return, the
history of the system stays next to the system, and "why is it like this?" has a
concrete answer. Reviewers can challenge a decision while it is being recorded.

The rule that accepted ADRs are immutable means a reader must sometimes follow a
supersede chain to find the current decision. We accept that cost because it
keeps the history honest.
