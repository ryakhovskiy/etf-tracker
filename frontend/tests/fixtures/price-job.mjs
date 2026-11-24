// A small job server for unit and browser fixtures. Unchanged versions stay
// pending until publish(), finish(), dispose(), or the request is aborted.
export function priceJob(initial) {
  let state = structuredClone({ version: 1, ...initial });
  const waiters = new Set();
  const snapshot = () => structuredClone(state);
  const publish = change => {
    state = structuredClone({ ...state, ...change, version: state.version + 1 });
    for (const waiter of [...waiters]) waiter.resolve(snapshot());
    return snapshot();
  };
  return {
    snapshot,
    publish,
    finish(items = state.items) { return publish({ status: 'DONE', items }); },
    get waiting() { return waiters.size; },
    async read(since, { signal } = {}) {
      if (signal?.aborted) throw new DOMException('Request aborted', 'AbortError');
      if (since == null || state.version > Number(since) || state.status === 'DONE') return snapshot();
      return new Promise((resolve, reject) => {
        const cleanup = () => { waiters.delete(waiter); signal?.removeEventListener('abort', abort); };
        const waiter = { resolve(value) { cleanup(); resolve(value); } };
        const abort = () => { cleanup(); reject(new DOMException('Request aborted', 'AbortError')); };
        waiters.add(waiter);
        signal?.addEventListener('abort', abort, { once: true });
      });
    },
    dispose() { publish({ status: 'DONE' }); },
  };
}
