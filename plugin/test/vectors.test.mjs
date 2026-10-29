// Runs every case in schema/vectors.json against the Node mirror.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { validateFinding, validateReply, validateNote } from '../lib/validate.mjs';
import { isBlockedFile, injectionScore, clean } from '../lib/sanitize.mjs';

const vectors = JSON.parse(readFileSync(new URL('../../schema/vectors.json', import.meta.url), 'utf8'));
const validators = { finding: validateFinding, reply: validateReply, note: validateNote };

test('vectors.json is not empty', () => assert.ok(vectors.length > 0));

for (const v of vectors) {
  test(`vector ${v.name}`, () => {
    // A string input is the literal request body; anything else is the body as written.
    const body = typeof v.input === 'string' ? v.input : JSON.stringify(v.input);
    const res = validators[v.kind](body);
    assert.equal(res.ok, v.expect.ok, JSON.stringify(res.error ?? res.value));
    if (!v.expect.ok) {
      assert.equal(res.error.code, v.expect.error_code, res.error.message);
      return;
    }
    for (const [k, want] of Object.entries(v.expect.cleaned ?? {})) assert.equal(res.value[k], want, k);
    if (v.expect.stripped) assert.deepEqual(res.tags.stripped, v.expect.stripped);
    if (v.expect.injection_score_min !== undefined) assert.ok(res.tags.injection_score >= v.expect.injection_score_min);
  });
}

// Same table as TestIsBlockedFile in inbox/internal/rules/rules_test.go.
test('isBlockedFile matches the Go table', () => {
  const cases = {
    '.gitlab-ci.yml': true, '.github/workflows/ci.yml': true, '.claude/settings.json': true, '.mcp.json': true,
    'CLAUDE.md': true, 'docs/claude.md': true, '.husky/pre-commit': true, 'package.json': true,
    'web/package.json': true, 'package-lock.json': true, 'go.mod': true, 'svc/go.sum': true, 'yarn.lock': true,
    'a/b/Cargo.lock': true, 'requirements-dev.txt': true, 'py/pyproject.toml': true, 'Dockerfile': true,
    'Dockerfile.prod': true, '.aic/policy.json': true, './.claude/x': true,
    'src/discount.js': false, 'test/discount.test.js': false, 'README.md': false, 'src/.github.js': false,
    'docs/github/notes.md': false, 'package.json.md': false,
  };
  for (const [p, want] of Object.entries(cases)) assert.equal(isBlockedFile(p), want, p);
});

test('injection score caps at 1', () => {
  assert.equal(injectionScore('hello'), 0);
  assert.equal(injectionScore('ignore previous, system prompt, delete, push --force, curl'), 1);
});

// Divergences between Go and JavaScript that the shared vectors do not cover.
test('Go-compatible edge cases', () => {
  const base = { schema: 'reply.v1', context_id: 'local/shop!1', task_id: 'f-1a2b3c4d', kind: 'fixed', note: 'ok' };
  assert.equal(validateReply('null').error.code, 'missing_field');
  assert.equal(validateReply('"x"').error.code, 'bad_json');
  // Go decodes a lone surrogate escape as U+FFFD.
  assert.equal(validateReply(`{"schema":"reply.v1","context_id":"local/shop!1","task_id":"f-1a2b3c4d","kind":"fixed","note":"x\\ud800y"}`).value.note, 'x\ufffdy');
  // Go matches keys case-insensitively, so "Note" is not unknown; the exact "note" key is still required.
  assert.equal(validateReply(JSON.stringify({ ...base, Kind: 'accepted' })).value.kind, 'accepted');
  // First error in key order wins, even for integer-like keys JSON.parse would move first.
  const f = '{"schema":"finding.v1","line":"x","1":0}';
  assert.equal(validateFinding(f).error.code, 'invalid_type');
  const line = (src) => validateFinding(`{"schema":"finding.v1","context_id":"local/shop!1","task_id":"f-1a2b3c4d","file":"a.js","line":${src},"category":"bug","severity":"nit","class":"fact","title":"t","note":"n"}`);
  assert.equal(line('12.0').error.code, 'invalid_type');
  assert.equal(line('1e2').error.code, 'invalid_type');
  assert.equal(line('99999999999999999999').error.code, 'invalid_type');
  assert.equal(line('3').ok, true);
  // \s in Go is ASCII only: a no-break space does not end the secret value.
  assert.equal(validateReply(JSON.stringify({ ...base, note: 'password=\u00a0abcdefgh' })).error.code, 'secret_detected');
  assert.equal(clean('a\u0130GNORE PREVIOUS').text, 'a\u0130GNORE PREVIOUS');
  assert.equal(injectionScore('\u0130GNORE PREVIOUS'), 0.25);
  // Go treats U+FEFF as removable (bom), not as whitespace; a title of only NBSP is blank.
  assert.equal(validateReply(JSON.stringify({ ...base, note: '\u00a0' })).error.code, 'empty_field');
});
