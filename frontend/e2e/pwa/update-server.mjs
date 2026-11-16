import { createServer } from 'node:http';

/** A per-test loopback proxy forwards the real production app (also Docker).
 * Service-worker update-script fetches cannot reliably use Playwright routes.
 * Here two builds are simulated at the HTTP boundary: only the worker's build
 * literal changes, plus a diagnostic MessagePort reply identifying the bytes.
 * Install/activate/fetch/SKIP_WAITING and the entire client UI stay production
 * code. No source files, shared server or production caches are mutated. */
export async function updateServer(upstreamURL) {
  const upstream = new URL(upstreamURL);
  let version = 1;
  const failures = [];
  const workerRequests = [];
  const requests = [];
  const stubs = new Map();
  const server = createServer(async (incoming, outgoing) => {
    try {
      const url = new URL(incoming.url, upstream);
      const stub = stubs.get(url.pathname);
      if (stub && Object.hasOwn(stub, 'body')) {
        const bytes = Buffer.isBuffer(stub.body) ? stub.body : Buffer.from(stub.body);
        outgoing.statusCode = stub.status ?? 200;
        outgoing.setHeader('content-type', stub.contentType ?? 'text/plain');
        outgoing.setHeader('cache-control', 'no-store');
        for (const [name, value] of Object.entries(stub.headers ?? {})) outgoing.setHeader(name, value);
        outgoing.setHeader('content-length', bytes.length);
        requests.push({ path: url.pathname, status: outgoing.statusCode });
        outgoing.end(bytes);
        return;
      }
      const headers = new Headers();
      for (const [name, value] of Object.entries(incoming.headers)) {
        if (!['host', 'connection', 'accept-encoding'].includes(name) && value !== undefined) {
          headers.set(name, Array.isArray(value) ? value.join(', ') : value);
        }
      }
      const chunks = [];
      for await (const chunk of incoming) chunks.push(chunk);
      const response = await fetch(stub?.forwardPath ? new URL(stub.forwardPath, upstream) : url, {
        method: incoming.method, headers, redirect: 'manual',
        body: ['GET', 'HEAD'].includes(incoming.method) ? undefined : Buffer.concat(chunks),
      });
      let bytes = Buffer.from(await response.arrayBuffer());
      if (url.pathname === '/sw.js' && response.status === 200) {
        const deployedVersion = version;
        workerRequests.push(deployedVersion);
        let source = bytes.toString('utf8');
        const versionLiteral = /(const VERSION\s*=\s*)(["'])([^"']+)\2/;
        if (!versionLiteral.test(source)) throw new Error('Update fixture needs the real worker build VERSION literal');
        source = source.replace(versionLiteral, (_, prefix, quote, build) => `${prefix}${quote}${build}-qa-${deployedVersion}${quote}`);
        source += `\n// QA deployment identity only; lifecycle behaviour above is unmodified.\nself.addEventListener('message', event => { if (event.data?.type === 'PWA_QA_VERSION') event.ports[0]?.postMessage(${deployedVersion}); });\n`;
        bytes = Buffer.from(source);
      }
      outgoing.statusCode = stub?.status ?? response.status;
      for (const [name, value] of response.headers) {
        // Node fetch decompresses the upstream body; never forward stale length
        // or encoding headers. The proxy also alters worker bytes deliberately.
        if (['content-encoding', 'content-length', 'transfer-encoding', 'connection', 'etag'].includes(name)) continue;
        if (name === 'location' && value.startsWith(upstream.origin)) outgoing.setHeader(name, value.slice(upstream.origin.length) || '/');
        else outgoing.setHeader(name, value);
      }
      if (stub?.contentType) outgoing.setHeader('content-type', stub.contentType);
      for (const [name, value] of Object.entries(stub?.headers ?? {})) outgoing.setHeader(name, value);
      outgoing.setHeader('content-length', bytes.length);
      requests.push({ path: url.pathname, status: outgoing.statusCode });
      outgoing.end(bytes);
    } catch (error) {
      failures.push(String(error));
      outgoing.statusCode = 502;
      outgoing.end('PWA test proxy failed');
    }
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    failures, workerRequests, requests,
    deploy(next) { version = next; },
    stub(path, response) { stubs.set(path, response); },
    async close() {
      server.closeAllConnections();
      await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
    },
  };
}

export async function workerVersion(page, which = 'controller') {
  return page.evaluate(async target => {
    const registration = await navigator.serviceWorker.ready;
    const worker = target === 'controller' ? navigator.serviceWorker.controller : registration.waiting;
    if (!worker) return null;
    return new Promise(resolve => {
      const channel = new MessageChannel();
      const timeout = setTimeout(() => { channel.port1.close(); resolve(null); }, 2000);
      channel.port1.onmessage = event => {
        clearTimeout(timeout);
        channel.port1.close();
        resolve(event.data);
      };
      worker.postMessage({ type: 'PWA_QA_VERSION' }, [channel.port2]);
    });
  }, which);
}
