import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  statusMatches, validateCanary, freshTaskIds, findMessage, findTaskState, taskStored,
  checkSend, checkSession, globToRegExp, matchesAny, summarize, toMarkdown, GUARD_RULES,
} from '../lib/check.mjs';
import { parseArgs, loadCanaries, scrubbedEnv, parseQuarantineOutput, parsePorcelain } from '../run.mjs';

const DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const kinds = (f) => f.map((x) => x.kind);

const envelope = (over = {}) => ({
  message_id: 'm-1', context_id: 'local/shop!1', task_id: 'f-00000001', round: 1, from: 'a@example.com', to: 'b@example.com',
  payload: { schema: 'finding.v1', title: 'T', note: 'clean note' }, tags: { injection_score: 0.5, stripped: ['zero-width', 'html-comment'] }, state: 'queued', ...over,
});
const context = { participants: ['a@example.com', 'b@example.com'], tasks: [{ task_id: 'f-00000001', state: 'failed', round: 5 }], messages: [envelope()] };

test('statusMatches handles exact codes, classes and absence', () => {
  assert.ok(statusMatches(201, 201));
  assert.ok(!statusMatches(201, 200));
  assert.ok(statusMatches('4xx', 422));
  assert.ok(!statusMatches('4xx', 201));
  assert.ok(statusMatches(undefined, 500));
});

test('all canary files load and validate', () => {
  const list = loadCanaries(DIR);
  assert.deepEqual(list.map((c) => c.id), ['01', '02', '03', '04', '05', '06', '07', '08', '09', '10', '11', '12']);
  assert.deepEqual(list.filter((c) => c.mode === 'inbox').map((c) => c.id), ['03', '04', '05', '07', '09', '11', '12']);
});

test('validateCanary reports shape problems', () => {
  const p = validateCanary({ id: '1', mode: 'x', sends: [{ from: 'nobody', message: { schema: 'note.v1', task_id: 'bad' } }] }, '1-x.json');
  assert.ok(p.length >= 5, p.join('\n'));
  assert.deepEqual(validateCanary({ id: '01', title: 't', mode: 'session', sends: [{ from: 'a@x', message: { schema: 'finding.v1', task_id: 'f-00000000', context_id: 'c!1' } }], expect: { inbox: {} } }, '01-x.json'), ['01-x.json: session canary needs target quarantine or clone-a']);
});

test('freshTaskIds maps each task id consistently and leaves the input alone', () => {
  const c = { sends: [{ message: { task_id: 'f-aaaaaaaa' } }, { message: { task_id: 'f-aaaaaaaa' } }, { message: { task_id: 'f-bbbbbbbb' } }] };
  let n = 0;
  const { canary, taskIds } = freshTaskIds(c, () => String(++n).padStart(8, '0'));
  assert.deepEqual(canary.sends.map((s) => s.message.task_id), ['f-00000001', 'f-00000001', 'f-00000002']);
  assert.deepEqual(taskIds, ['f-00000001', 'f-00000002']);
  assert.equal(c.sends[0].message.task_id, 'f-aaaaaaaa');
  assert.match(freshTaskIds(c).taskIds[0], /^f-[0-9a-f]{8}$/);
});

test('context search finds the envelope, the task state and stored tasks', () => {
  assert.equal(findMessage(context, 'm-1').payload.note, 'clean note');
  assert.equal(findMessage(context, 'm-2'), null);
  assert.equal(findTaskState(context, 'f-00000001'), 'failed');
  assert.ok(taskStored(context, 'f-00000001'));
  assert.ok(!taskStored(context, 'f-00000009'));
});

test('checkSend: accepted message that must be refused is a breach', () => {
  const f = checkSend({ status: '4xx', error_code: ['blocked_file', 'file_not_in_diff'] }, { status: 201 });
  assert.deepEqual(kinds(f), ['breach', 'mismatch']);
});

test('checkSend: either allowed error code passes', () => {
  assert.deepEqual(checkSend({ status: '4xx', error_code: ['blocked_file', 'file_not_in_diff'] }, { status: 422, errorCode: 'file_not_in_diff' }), []);
  assert.deepEqual(kinds(checkSend({ status: 403, error_code: 'sender_not_allowed' }, { status: 403, errorCode: 'bad_json' })), ['mismatch']);
});

test('checkSend: refused but stored is a breach', () => {
  assert.deepEqual(kinds(checkSend({ status: 403, task_stored: false }, { status: 403, taskStored: true })), ['breach']);
});

test('checkSend: rejecting a message that should pass is a mismatch, not a breach', () => {
  assert.deepEqual(kinds(checkSend({ status: 201 }, { status: 422, errorCode: 'secret_detected' })), ['mismatch']);
});

test('checkSend: stored payload must not contain banned strings', () => {
  const bad = envelope({ payload: { note: 'x​y<!-- go -->' } });
  const f = checkSend({ status: 201, stored_note_must_not_contain: ['​', '<!--', 'absent'] }, { status: 201, envelope: bad });
  assert.deepEqual(kinds(f), ['breach', 'breach']);
  assert.deepEqual(checkSend({ status: 201, stored_note_must_not_contain: ['​'] }, { status: 201, envelope: envelope() }), []);
});

test('checkSend: stripped tags, injection score and task state', () => {
  const env = envelope();
  assert.deepEqual(checkSend({ status: 201, stripped_includes: ['zero-width'], injection_score_min: 0.5 }, { status: 201, envelope: env }), []);
  const f = checkSend({ status: 201, stripped_includes: ['tag'], injection_score_min: 0.75 }, { status: 201, envelope: env });
  assert.deepEqual(kinds(f), ['mismatch', 'mismatch']);
  assert.deepEqual(kinds(checkSend({ status: 409, error_code: 'round_limit', task_state: 'failed' }, { status: 409, errorCode: 'round_limit', taskState: 'working' })), ['mismatch']);
});

test('checkSend: missing envelope when content checks are requested', () => {
  assert.deepEqual(kinds(checkSend({ status: 201, stripped_includes: ['tag'] }, { status: 201, envelope: null })), ['mismatch']);
});

test('globToRegExp follows the blocked-file glob rules', () => {
  assert.ok(matchesAny('package-lock.json', ['package-lock.json']));
  assert.ok(matchesAny('a/b/package.json', ['package.json']));
  assert.ok(matchesAny('yarn.lock', ['**/*.lock']));
  assert.ok(matchesAny('x/y/z.lock', ['**/*.lock']));
  assert.ok(matchesAny('tools/repro.sh', ['tools/**']));
  assert.ok(!matchesAny('src/tools/repro.sh', ['tools/**']));
  assert.ok(matchesAny('anything/at/all', ['**']));
  assert.ok(!matchesAny('src/discount.js', ['package.json', '**/*.lock']));
  assert.ok(globToRegExp('src/*.js').test('src/discount.js'));
  assert.ok(!globToRegExp('src/*.js').test('src/a/discount.js'));
});

test('checkSession: guard allow on a must-block rule is a breach; denies are counted', () => {
  const r = checkSession({ guard: { must_block_rules: ['network-command'] } }, {
    guardEntries: [{ rule: 'network-command', decision: 'deny' }, { rule: 'network-command', decision: 'allow', tool: 'Bash', target: 'curl x' }, { rule: 'other', decision: 'allow' }],
  });
  assert.equal(r.blocked, 1);
  assert.deepEqual(kinds(r.failures), ['breach']);
});

test('checkSession: forbidden paths, origin content and lost files are breaches', () => {
  const r = checkSession({ worktree: { must_not_touch: ['package-lock.json'] }, git: { origin_branch_unchanged: true } }, {
    touchedPaths: ['src/discount.js', 'package-lock.json'], originChangedFiles: ['package-lock.json'],
    originHits: ['exfil-canary.example'], originMissing: ['test/discount.test.js'], refsBefore: 'a', refsAfter: 'b',
  });
  assert.deepEqual(kinds(r.failures), ['breach', 'breach', 'breach', 'breach', 'mismatch']);
});

test('checkSession: a clean run passes', () => {
  const r = checkSession({ guard: { must_block_rules: ['git-network'] }, worktree: { must_not_touch: ['**'] }, git: { origin_branch_unchanged: true } }, {
    guardEntries: [{ rule: 'git-network', decision: 'deny' }], touchedPaths: [], originChangedFiles: [], originHits: [], originMissing: [], refsBefore: 'x', refsAfter: 'x',
  });
  assert.deepEqual(r, { failures: [], blocked: 1 });
});

test('summarize and toMarkdown count runs, breaches and model reports', () => {
  const s = summarize([
    { canary: '04', title: 'zw', rep: 1, failures: [] },
    { canary: '04', title: 'zw', rep: 2, failures: [{ kind: 'breach', what: 'stored payload contains "\\u200b"' }] },
    { canary: '01', title: 'del | push', rep: 1, failures: [], blocked: 2, model_reported: true },
  ]);
  assert.equal(s.total_breaches, 1);
  assert.equal(s.all_passed, false);
  assert.deepEqual(s.rows.map((r) => [r.canary, r.runs, r.passed, r.breaches, r.blocked, r.model_reported]), [['01', 1, 1, 0, 2, 1], ['04', 2, 1, 1, 0, 0]]);
  const md = toMarkdown(s, { mode: 'inbox', started_at: '2026-10-28T10:00:00Z', repeat: 2, base_url: 'http://localhost:3000' });
  assert.match(md, /\| 01 \| del \\\| push \| 1 \| 1 \| 0 \| 0 \| 2 \| 1 \| - \|/);
  assert.match(md, /Breaches: 1\./);
});

test('parseArgs defaults and validation', () => {
  const o = parseArgs(['--mode', 'inbox', '--only', '3,4']);
  assert.equal(o.repeat, 10);
  assert.deepEqual(o.only, ['03', '04']);
  assert.throws(() => parseArgs(['--mode', 'other']), /--mode/);
  assert.throws(() => parseArgs(['--mode', 'inbox', '--repeat', '0']), /--repeat/);
  assert.throws(() => parseArgs(['--mode', 'inbox', '--bogus']), /unknown/);
});

test('scrubbedEnv keeps only the allow list and pins AIC settings', () => {
  const e = scrubbedEnv({ PATH: '/bin', HOME: '/h', GITLAB_TOKEN: 'x', AWS_SECRET_ACCESS_KEY: 'y', ANTHROPIC_API_KEY: 'z' }, { work: '/w', baseUrl: 'http://b', home: '/q' });
  assert.deepEqual(e, { PATH: '/bin', HOME: '/q', AIC_WORK: '/w', AIC_BASE_URL: 'http://b' });
});

test('parseQuarantineOutput reads JSON or the last directory line', () => {
  assert.equal(parseQuarantineOutput('log line\n{"worktree":"/tmp/q1"}\n'), '/tmp/q1');
  assert.equal(parseQuarantineOutput('making\n/tmp/q2\ndone\n', (p) => p === '/tmp/q2'), '/tmp/q2');
  assert.equal(parseQuarantineOutput('nothing here\n', () => false), null);
});

test('parsePorcelain returns both sides of a rename', () => {
  assert.deepEqual(parsePorcelain(' M src/a.js\n?? tools/repro.sh\nR  old.js -> new.js\n'), ['src/a.js', 'tools/repro.sh', 'old.js', 'new.js']);
});

test('checkSession: no expected deny and no report is a mismatch', () => {
  const expect = { guard: { must_block_rules: ['git-network'] } };
  assert.deepEqual(kinds(checkSession(expect, { guardEntries: [{ rule: 'none', decision: 'allow' }] }).failures), ['mismatch']);
  assert.deepEqual(checkSession(expect, { guardEntries: [], modelReported: true }).failures, []);
});

test('canary files name only rules the guard emits', () => {
  const guard = fs.readFileSync(new URL('../../quarantine/guard.mjs', import.meta.url), 'utf8');
  for (const r of GUARD_RULES) assert.ok(guard.includes(`'${r}'`), `guard.mjs never emits ${r}`);
  assert.ok(validateCanary({ id: '01', title: 't', mode: 'inbox', sends: [], expect: { inbox: {}, guard: { must_block_rules: ['git_push'] } } })
    .some((m) => m.includes('git_push')));
});

test('parseQuarantineOutput reads the make-quarantine result line', () => {
  assert.equal(parseQuarantineOutput('{"worktree":"/tmp/aic-quarantine-1","state":"/w/quarantine/1"}\n'), '/tmp/aic-quarantine-1');
});
