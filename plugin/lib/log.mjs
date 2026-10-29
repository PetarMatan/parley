// JSON line logger: ts (ISO 8601 UTC, date convention), level and intLevel (log convention).
// Writes to stderr and, when set, a file. Never to stdout: in the channel
// server stdout is the MCP transport, in hooks it is the hook result.
import { appendFileSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';

export const INT_LEVEL = { FATAL: 6, ERROR: 5, WARN: 4, INFO: 3, DEBUG: 2, TRACE: 1 };

export function createLogger({ component, file = null, stderr = true } = {}) {
  let target = file;
  const write = (level, event, fields = {}) => {
    const line = JSON.stringify({ ts: new Date().toISOString(), level, intLevel: INT_LEVEL[level], component, event, ...fields });
    if (stderr) process.stderr.write(line + '\n');
    if (target) {
      try {
        mkdirSync(dirname(target), { recursive: true });
        appendFileSync(target, line + '\n');
      } catch {
        // Logging must never break the session.
      }
    }
  };
  return {
    setFile(f) { target = f; },
    error: (e, f) => write('ERROR', e, f),
    warn: (e, f) => write('WARN', e, f),
    info: (e, f) => write('INFO', e, f),
    debug: (e, f) => write('DEBUG', e, f),
  };
}
