// Deterministic callback/queue regressions against the actual TypeScript source.
// No browser timing or additional test dependencies are required.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const test = require("node:test");
const ts = require("../web/node_modules/typescript");

const root = path.resolve(__dirname, "..");
const source = (name) =>
  fs.readFileSync(path.join(root, "web/src", name), "utf8");
const compile = (code) =>
  ts.transpileModule(code, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
const tick = () => new Promise((resolve) => setImmediate(resolve));
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

function queueWith(adapter) {
  const states = [];
  const react = {
    useState(value) {
      const index = states.push(value) - 1;
      return [
        value,
        (next) => {
          states[index] =
            typeof next === "function" ? next(states[index]) : next;
        },
      ];
    },
    useRef: (current) => ({ current }),
    useCallback: (fn) => fn,
    useMemo: (fn) => fn(),
  };
  const exports = {};
  vm.runInNewContext(compile(source("transferQueue.ts")), {
    exports,
    AbortController,
    require: (name) =>
      name === "react"
        ? react
        : { isAbortError: (e) => e?.name === "AbortError" },
  });
  const queue = exports.useTransferQueue(adapter);
  queue.addFiles([{ name: "sample.txt", type: "text/plain", size: 1 }]);
  return { queue, states };
}

test("late create from a cancelled run cannot replace the newer retry target", async () => {
  const first = deferred(),
    second = deferred();
  const uploads = [];
  let created = 0;
  const { queue } = queueWith({
    create: () => (++created === 1 ? first.promise : second.promise),
    upload: async (id) => {
      uploads.push(id);
      throw Error("retryable");
    },
    cancel: async () => {},
    publish: async () => assert.fail("upload did not succeed"),
  });
  queue.start();
  await queue.cancel();
  queue.start();
  second.resolve("new");
  await tick();
  first.resolve("cancelled");
  await tick();
  queue.retry();
  await tick();
  assert.deepEqual(uploads, ["new", "new"]);
});

for (const stage of ["create", "upload", "publish"]) {
  test(`late ${stage} failure cannot mutate the restarted queue`, async () => {
    const old = deferred(),
      next = deferred();
    let created = 0;
    const { queue, states } = queueWith({
      create: () => {
        if (++created > 1) return next.promise;
        return stage === "create" ? old.promise : Promise.resolve("old");
      },
      upload: () => (stage === "upload" ? old.promise : Promise.resolve()),
      publish: () => old.promise,
      cancel: async () => {},
    });
    queue.start();
    await tick();
    await queue.cancel();
    queue.start();
    old.reject(Error("late failure"));
    await tick();
    assert.equal(states[1], "uploading");
    assert.equal(states[0][0].status, "staged");
    assert.equal(states[3], "");
  });
}

// Extract the callback so the test exercises the UI's commit guard rather than
// a duplicate implementation. Ref updates below model a subsequent React render.
function remoteEditor() {
  const text = source("App.tsx");
  const ast = ts.createSourceFile(
    "App.tsx",
    text,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  let callback;
  function visit(node) {
    if (
      ts.isVariableDeclaration(node) &&
      node.name.getText(ast) === "followRemotePaste"
    ) {
      callback = node.initializer.arguments[0].getText(ast);
    }
    ts.forEachChild(node, visit);
  }
  visit(ast);
  assert.ok(callback, "the production refresh callback exists");
  const requests = [];
  const draft = { id: "one", title: "title", text: "saved", tags: "" };
  const editorStateRef = {
    current: { selectedPasteId: "one", editDraft: draft },
  };
  const result = { draft, notice: "", pastes: null };
  const follow = vm.runInNewContext(
    compile(`const follow = ${callback}; follow;`),
    {
      editorStateRef,
      remoteRefreshGeneration: { current: 0 },
      client: {
        paste: () => {
          const pending = deferred();
          requests.push(pending);
          return pending.promise;
        },
      },
      query: "",
      filter: "all",
      tagFilter: "",
      searchParams: () => "",
      setPastes: (pastes) => {
        result.pastes =
          typeof pastes === "function" ? pastes(result.pastes ?? []) : pastes;
      },
      setEditDraft: (value) => {
        result.draft = value;
      },
      setRemoteDraftNotice: (value) => {
        result.notice = value;
      },
    },
  );
  const response = (text) => ({ id: "one", title: "title", text, tags: [] });
  return { follow, requests, editorStateRef, result, response };
}

test("remote refresh preserves typing while its request was pending", async () => {
  const h = remoteEditor();
  const pending = h.follow("one");
  const typed = { ...h.result.draft, text: "unsaved typing" };
  h.result.draft = typed;
  h.editorStateRef.current = { selectedPasteId: "one", editDraft: typed };
  h.requests[0].resolve(h.response("remote"));
  await pending;
  assert.equal(h.result.draft.text, "unsaved typing");
  assert.equal(h.result.notice, "one");
});

test("remote refresh cannot write into a different selected record", async () => {
  const h = remoteEditor();
  const pending = h.follow("one");
  const selected = { ...h.result.draft, id: "two", text: "second record" };
  h.result.draft = selected;
  h.editorStateRef.current = { selectedPasteId: "two", editDraft: selected };
  h.requests[0].resolve(h.response("remote"));
  await pending;
  assert.equal(h.result.draft.id, "two");
  assert.equal(h.result.pastes, null);
});

test("newer refresh wins when responses arrive out of order", async () => {
  const h = remoteEditor();
  const first = h.follow("one"),
    second = h.follow("one");
  h.requests[1].resolve(h.response("newest"));
  await second;
  h.requests[0].resolve(h.response("older"));
  await first;
  assert.equal(h.result.draft.text, "newest");
});

test("explicit refresh applies when the draft stayed untouched", async () => {
  const h = remoteEditor();
  const pending = h.follow("one");
  h.requests[0].resolve(h.response("newest"));
  await pending;
  assert.equal(h.result.draft.text, "newest");
});

for (const stage of ["upload", "publish"]) {
  test(`late ${stage} success and progress cannot mutate the restarted queue`, async () => {
    const old = deferred(),
      next = deferred();
    let created = 0,
      progress;
    const { queue, states } = queueWith({
      create: () => (++created === 1 ? Promise.resolve("old") : next.promise),
      upload: (id, item, file, onProgress) => {
        progress = onProgress;
        return stage === "upload" ? old.promise : Promise.resolve();
      },
      publish: () => old.promise,
      cancel: async () => {},
    });
    queue.start();
    await tick();
    await queue.cancel();
    queue.start();
    progress(1, 1);
    old.resolve({ url: "old-share" });
    await tick();
    assert.equal(states[1], "uploading");
    assert.equal(states[0][0].status, "staged");
    assert.equal(states[0][0].loaded, 0);
    assert.equal(states[2], null);
  });
}
