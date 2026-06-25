// Boots every device type once to verify the factories produce a clean,
// commissionable ServerNode. Not embedded (underscore prefix). Run with:
//   node _selftest.mjs
process.env.MATTER_LOG_LEVEL = 'fatal';
process.env.MATTER_STORAGE_PATH = './.selftest-storage';

const { buildDevice } = await import('./lib/devices/index.mjs');
const { rmSync } = await import('node:fs');

const types = [
  'tempsensor', 'lightsensor', 'smartplug', 'evse',
  'thermostat', 'heatpump', 'dishwasher', 'laundrywasher',
];

let port = 5700;
let failures = 0;

for (const type of types) {
  const params = {
    nodeId: `selftest-${type}`,
    type,
    serial: `SELFTEST-${type}`,
    port: port++,
    passcode: 20202021,
    discriminator: 3840,
    deviceName: type,
    defaults: {},
  };
  try {
    const dev = await buildDevice(params, () => {});
    const ok = dev.pairingCode && dev.qrCode;
    console.log(`${ok ? 'OK ' : 'NO-CODE'} ${type.padEnd(14)} pairing=${dev.pairingCode} qr=${dev.qrCode.slice(0, 16)}...`);
    if (!ok) failures++;
    await dev.node.close();
  } catch (e) {
    failures++;
    console.log(`FAIL ${type.padEnd(14)} ${String(e && e.message || e)}`);
    const causes = collectCauses(e);
    for (const c of causes) console.log(`     cause: ${c}`);
  }
}

function collectCauses(e, depth = 0, out = []) {
  if (!e || depth > 6) return out;
  if (e.message) out.push(e.message.split('\n')[0]);
  if (Array.isArray(e.errors)) for (const sub of e.errors) collectCauses(sub, depth + 1, out);
  if (e.cause) collectCauses(e.cause, depth + 1, out);
  return out;
}

try { rmSync('./.selftest-storage', { recursive: true, force: true }); } catch {}

console.log(`\n${types.length - failures}/${types.length} device types booted cleanly`);
process.exit(failures > 0 ? 1 : 0);
