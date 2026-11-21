// Signs an update descriptor for the app's updater (shell/src/stremio_app/updater.rs):
// writes <descriptor>.sig, the base64 ed25519 signature of the file's bytes.
// The private key (PKCS#8 PEM) comes from UPDATE_SIGNING_KEY, or from the file
// named by UPDATE_SIGNING_KEY_FILE. It is never printed.
//
// Usage: node scripts/sign-descriptor.mjs out/descriptor.json
import { createPrivateKey, createPublicKey, sign, verify } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';

const [file] = process.argv.slice(2);
if (!file) throw new Error('usage: sign-descriptor.mjs <descriptor.json>');
const pem = process.env.UPDATE_SIGNING_KEY
    || (process.env.UPDATE_SIGNING_KEY_FILE && readFileSync(process.env.UPDATE_SIGNING_KEY_FILE, 'utf8'));
if (!pem) throw new Error('set UPDATE_SIGNING_KEY or UPDATE_SIGNING_KEY_FILE');

const key = createPrivateKey(pem);
const data = readFileSync(file);
const signature = sign(null, data, key);

// The app only accepts the key in shell/update-key.pub: refuse to sign with another.
const shipped = readFileSync(new URL('../shell/update-key.pub', import.meta.url), 'utf8').trim();
const ours = createPublicKey(key).export({ type: 'spki', format: 'der' }).subarray(-32).toString('base64');
if (ours !== shipped) throw new Error('the signing key does not match shell/update-key.pub');
if (!verify(null, data, createPublicKey(key), signature)) throw new Error('signature check failed');

writeFileSync(`${file}.sig`, `${signature.toString('base64')}\n`);
console.log(`signed ${file}`);
