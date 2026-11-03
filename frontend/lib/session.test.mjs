import assert from "node:assert/strict";
import { test } from "node:test";
import { authenticatedFetch, withSessionLock } from "./session.ts";

test("concurrent unauthorized requests refresh only once", async (t) => {
  let refreshed = false;
  let refreshes = 0;
  t.mock.method(globalThis, "fetch", async (url, init) => {
    assert.equal(init.credentials, "include");
    if (url.endsWith("/auth/refresh")) {
      assert.equal(init.method, "POST");
      refreshes++;
      refreshed = true;
      return new Response(null, { status: 204 });
    }
    return new Response(null, { status: refreshed ? 200 : 401 });
  });
  const responses = await Promise.all([authenticatedFetch("/auth/me"), authenticatedFetch("/auth/me")]);
  assert.equal(refreshes, 1);
  assert.ok(responses.every((response) => response.status === 200));
});

test("refresh outage is returned without pretending authentication failed", async (t) => {
  let calls = 0;
  t.mock.method(globalThis, "fetch", async (url) => {
    calls++;
    return new Response(null, { status: url.endsWith("/auth/refresh") ? 503 : 401 });
  });
  assert.equal((await authenticatedFetch("/auth/me")).status, 503);
  assert.equal(calls, 3);
});

test("failed session operation does not block a subsequent logout retry", async () => {
  await assert.rejects(withSessionLock(async () => { throw new Error("storage unavailable"); }));
  assert.equal(await withSessionLock(async () => "retried"), "retried");
});

test("Web Locks serializes refresh and logout across module instances", async (t) => {
  let queue = Promise.resolve();
  let locks = 0;
  const original = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { locks: {
    request(name, action) {
      assert.equal(name, "holdings-session");
      locks++;
      const result = queue.then(action);
      queue = result.catch(() => undefined);
      return result;
    },
  } } });
  t.after(() => {
    if (original) Object.defineProperty(globalThis, "navigator", original);
    else delete globalThis.navigator;
  });
  // Separate module state represents two tabs, sharing only the browser lock.
  const tabA = await import("./session.ts?tab=a");
  const tabB = await import("./session.ts?tab=b");
  const actions = [];
  await Promise.all([
    tabA.withSessionLock(async () => { actions.push("refresh-start"); await Promise.resolve(); actions.push("refresh-end"); }),
    tabB.withSessionLock(async () => { actions.push("logout"); }),
  ]);
  assert.deepEqual(actions, ["refresh-start", "refresh-end", "logout"]);
  assert.equal(locks, 2);
});
