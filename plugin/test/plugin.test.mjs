// The plugin package (D49): the manifest, the hooks file, the plugin-level
// MCP file and the marketplace file at the repository root parse, name only
// files that exist, and reach everything through ${CLAUDE_PLUGIN_ROOT}.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

const PLUGIN = new URL('../', import.meta.url).pathname;
const ROOT = join(PLUGIN, '..');
const json = (p) => JSON.parse(readFileSync(p, 'utf8'));
const ROOT_VAR = '${CLAUDE_PLUGIN_ROOT}';

test('plugin.json names the plugin parley and points at the hooks and MCP files', () => {
  const m = json(join(PLUGIN, '.claude-plugin', 'plugin.json'));
  assert.equal(m.name, 'parley');
  assert.match(m.version, /^\d+\.\d+\.\d+$/);
  assert.equal(m.hooks, './hooks/hooks.json');
  assert.equal(m.mcpServers, './.mcp.json');
  assert.ok(existsSync(join(PLUGIN, m.hooks)));
  assert.ok(existsSync(join(PLUGIN, m.mcpServers)));
});

test('the MCP server and every hook run from the plugin root, and the scripts exist', () => {
  const mcp = json(join(PLUGIN, '.mcp.json'));
  assert.deepEqual(Object.keys(mcp.mcpServers), ['aic-inbox'], 'the server key is the tool prefix mcp__plugin_parley_aic-inbox__');
  const server = mcp.mcpServers['aic-inbox'];
  assert.equal(server.command, 'node');
  assert.equal(server.args.length, 1);
  assert.ok(server.args[0].startsWith(ROOT_VAR + '/'));
  assert.ok(existsSync(join(PLUGIN, server.args[0].slice(ROOT_VAR.length + 1))));

  const hooks = json(join(PLUGIN, 'hooks', 'hooks.json')).hooks;
  assert.deepEqual(Object.keys(hooks).sort(), ['CwdChanged', 'SessionEnd', 'SessionStart']);
  for (const [event, matchers] of Object.entries(hooks)) {
    for (const h of matchers.flatMap((m) => m.hooks)) {
      assert.equal(h.type, 'command');
      const m = /^node "\$\{CLAUDE_PLUGIN_ROOT\}\/(hooks\/[a-z-]+\.mjs)"$/.exec(h.command);
      assert.ok(m, `${event}: ${h.command}`);
      assert.ok(existsSync(join(PLUGIN, m[1])), `${event}: ${m[1]} exists`);
    }
  }
});

test('the skills ship inside the plugin and the marketplace at the repository root lists it', () => {
  for (const skill of ['review-send', 'review-handle', 'login']) {
    const text = readFileSync(join(PLUGIN, 'skills', skill, 'SKILL.md'), 'utf8');
    assert.match(text, new RegExp(`^name: ${skill}$`, 'm'));
  }
  assert.match(readFileSync(join(PLUGIN, 'skills', 'login', 'SKILL.md'), 'utf8'), /\$\{CLAUDE_PLUGIN_ROOT\}\/login\.mjs/);
  const mk = json(join(ROOT, '.claude-plugin', 'marketplace.json'));
  assert.equal(mk.name, 'parley', 'the channel flag is plugin:parley@parley');
  const entry = mk.plugins.find((p) => p.name === 'parley');
  assert.equal(entry.source, './plugin');
  assert.equal(entry.version, json(join(PLUGIN, '.claude-plugin', 'plugin.json')).version);
  assert.ok(existsSync(join(PLUGIN, 'package-lock.json')), 'Claude Code installs the dependencies from the lockfile');
});
