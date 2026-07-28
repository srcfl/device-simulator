// Device Simulator — matter.js bridge.
//
// This Node program is spawned and supervised by the Go side. It speaks
// newline-delimited JSON-RPC over stdio:
//   * stdin  : requests  { id, method, params }
//   * stdout : responses { id, result } | { id, error }  AND events { event, ... }
//   * stderr : human-readable diagnostics ONLY (never control frames)
//
// One process hosts many matter.js ServerNodes, keyed by nodeId. Device-type
// specifics live in ./lib/devices/*.mjs; this file is just the transport +
// dispatch and is intentionally free of matter.js cluster knowledge.

import './lib/log.mjs'; // must be first: pins matter.js logging to stderr
import readline from 'node:readline';
import { createDevice, removeDevice, setAttributes, getState, shutdownAll } from './lib/registry.mjs';

function send(obj) {
  process.stdout.write(JSON.stringify(obj) + '\n');
}

// emit is passed down to device modules so they can push events (commissioning
// changes, external attribute writes, logs) back to Go.
export function emit(eventObj) {
  send(eventObj);
}

function logErr(message) {
  process.stderr.write(`[matter-bridge] ${message}\n`);
}

const handlers = {
  async createDevice(params) {
    return await createDevice(params, emit);
  },
  async setAttributes(params) {
    return await setAttributes(params);
  },
  async getState(params) {
    return await getState(params);
  },
  async removeDevice(params) {
    return await removeDevice(params);
  },
  async shutdown() {
    await shutdownAll();
    // Allow the response to flush before exiting.
    setImmediate(() => process.exit(0));
    return { ok: true };
  },
};

async function dispatch(msg) {
  const handler = handlers[msg.method];
  if (!handler) {
    send({ id: msg.id, error: `unknown method: ${msg.method}` });
    return;
  }
  try {
    const result = await handler(msg.params || {});
    send({ id: msg.id, result });
  } catch (e) {
    send({ id: msg.id, error: String((e && e.message) || e) });
  }
}

const rl = readline.createInterface({ input: process.stdin });
rl.on('line', (line) => {
  const text = line.trim();
  if (!text) return;
  let msg;
  try {
    msg = JSON.parse(text);
  } catch {
    logErr(`ignoring non-JSON line: ${text.slice(0, 120)}`);
    return;
  }
  dispatch(msg);
});

rl.on('close', async () => {
  // stdin closed (Go side went away) — shut down cleanly.
  try {
    await shutdownAll();
  } catch (e) {
    logErr(`shutdown on stdin close failed: ${e}`);
  }
  process.exit(0);
});

process.on('uncaughtException', (err) => {
  logErr(`uncaughtException: ${err && err.stack ? err.stack : err}`);
});
process.on('unhandledRejection', (err) => {
  logErr(`unhandledRejection: ${err && err.stack ? err.stack : err}`);
});

// Announce readiness once the runtime is up.
send({ event: 'ready', bridge: 'device-simulator', node: process.version });
