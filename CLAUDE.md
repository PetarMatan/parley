# Parley

A channel that lets one person's coding agent talk to another person's coding agent. Read [VISION.md](VISION.md) before any feature, design, or scope decision.

## Where to look

- What the product is for and the rules a decision must pass: [VISION.md](VISION.md)
- Where the work stands and what comes next: the "Status" section of [parley-testbed.md](parley-testbed.md)
- Why something was decided the way it was: [DECISIONS.md](DECISIONS.md)
- Message shapes, cleaning rules, routing and state rules: [docs/contract.md](docs/contract.md)
- Logging, dates, authentication, HTTP API, and PII rules: [docs/conventions.md](docs/conventions.md)
- How to run it, locally and on the testbed: [docs/runbook.md](docs/runbook.md)
- Layout, configuration, and the HTTP API: [README.md](README.md), [inbox/openapi.yaml](inbox/openapi.yaml)

## Words

VISION.md fixes five words: person, agent, channel, record, agent conversation. Use them with that meaning in every document, comment, and message. Do not introduce a synonym.

## Rules that are easy to break

- The Go validator and the Node validator must behave identically. Both are tested against `schema/vectors.json`.
- Never edit `schema/vectors.json` by hand. Edit `schema/gen-vectors.mjs` and run `make vectors`.
- A changed message rule is changed in both validators and gets a new vector in the generator.
- Keep documents company-neutral: name public products and standards only.

## Decisions

Record every design decision as a new row in [DECISIONS.md](DECISIONS.md), in the section it belongs to and with that section's id prefix. Three columns: what was open, what was chosen, why. Check the choice against the rules in VISION.md first.

## Done means

`make test` runs and every suite is green. A change is not reported as done before that.
