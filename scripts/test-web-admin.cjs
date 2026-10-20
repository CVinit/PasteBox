// Exercise real management page loading under reordered responses and render the
// audit component with long identifiers in each locale.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const test = require("node:test");
const ts = require("../web/node_modules/typescript");
const React = require("../web/node_modules/react");
const {
  renderToStaticMarkup,
} = require("../web/node_modules/react-dom/server");
const source = fs.readFileSync(
  require("node:path").join(__dirname, "../web/src/AdminManagement.tsx"),
  "utf8",
);
const compiled = ts.transpileModule(source, {
  fileName: "AdminManagement.tsx",
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2022,
    jsx: ts.JsxEmit.ReactJSX,
  },
}).outputText;
function load(react = React) {
  const exports = {};
  vm.runInNewContext(compiled + "\nexports.useAdminPage = useAdminPage;", {
    exports,
    AbortController,
    Error,
    require: (name) => {
      if (name === "react") return react;
      if (name === "react/jsx-runtime")
        return require("../web/node_modules/react/jsx-runtime");
      if (name === "lucide-react")
        return require("../web/node_modules/lucide-react");
      if (name === "./api") return { formatBytes: (size) => `${size} B` };
      throw new Error(`Unexpected import: ${name}`);
    },
  });
  return exports;
}
const tick = () => new Promise((resolve) => setImmediate(resolve));
function pagedList() {
  const state = [],
    effects = [],
    pending = [];
  let cursor = 0;
  const react = {
    useState(initial) {
      const index = cursor++;
      if (!(index in state)) state[index] = initial;
      return [
        state[index],
        (next) => {
          state[index] = typeof next === "function" ? next(state[index]) : next;
        },
      ];
    },
    useEffect(effect, dependencies) {
      const index = cursor++;
      const old = effects[index];
      if (
        !old ||
        dependencies.some((value, i) => value !== old.dependencies[i])
      ) {
        old?.cleanup?.();
        effects[index] = { dependencies, cleanup: effect() };
      }
    },
  };
  const { useAdminPage } = load(react);
  const loader = (query, offset, signal) =>
    new Promise((resolve, reject) =>
      pending.push({ query, offset, signal, resolve, reject }),
    );
  return {
    pending,
    render() {
      cursor = 0;
      return useAdminPage(loader);
    },
    unmount() {
      effects.forEach((effect) => effect?.cleanup?.());
    },
  };
}

test("admin search resets paging and rejects late results from the previous query", async () => {
  const list = pagedList();
  let page = list.render();
  page.go(2);
  page = list.render();
  assert.equal(list.pending[1].offset, 40);
  page.search("  Alice  ");
  page = list.render();
  assert.equal(list.pending[2].query, "Alice");
  assert.equal(list.pending[2].offset, 0);
  assert.equal(list.pending[0].signal.aborted, true);
  assert.equal(list.pending[1].signal.aborted, true);
  list.pending[2].resolve(
    Array.from({ length: 21 }, (_, id) => ({ id: `new-${id}` })),
  );
  await tick();
  page = list.render();
  assert.equal(page.rows.length, 20);
  assert.equal(page.hasNext, true);
  list.pending[0].resolve([{ id: "stale" }]);
  list.pending[1].reject(new Error("stale error"));
  await tick();
  page = list.render();
  assert.equal(page.rows[0].id, "new-0");
  assert.equal(page.error, "");
  assert.equal(page.loading, false);
  list.unmount();
});

test("failed admin page loads display an error and do not keep another page's records", async () => {
  const list = pagedList();
  list.render();
  list.pending[0].resolve([{ id: "first" }]);
  await tick();
  let page = list.render();
  assert.equal(page.hasNext, false);
  page.reload();
  list.render();
  list.pending[1].reject(new Error("service unavailable"));
  await tick();
  page = list.render();
  assert.equal(page.rows.length, 0);
  assert.equal(page.error, "service unavailable");
  assert.equal(page.loading, false);
  page.reload();
  list.render();
  list.unmount();
  assert.equal(list.pending[2].signal.aborted, true);
  list.pending[2].reject(new Error("after unmount"));
  await tick();
});

test("audit table localizes known actions, paginates rows and preserves full long identifiers", () => {
  const { AdminAuditLog, adminCopy } = load();
  for (const locale of ["en", "zh-CN", "zh-TW", "es"]) {
    const identifier = "usr_" + "1234567890".repeat(12);
    const logs = Array.from({ length: 25 }, (_, id) => ({
      id: String(id),
      action: "admin.catalog_entries_update",
      actorId: identifier,
      target: identifier,
      createdAt: "2026-10-19T10:00:00Z",
    }));
    const t = (key) => adminCopy[locale][key] ?? key;
    const html = renderToStaticMarkup(
      React.createElement(AdminAuditLog, {
        logs,
        locale,
        t,
        fallbackLabel: (action) => action,
      }),
    );
    assert.equal(
      (html.match(/<tbody>[\s\S]*?<\/tbody>/)?.[0].match(/<tr>/g) ?? []).length,
      20,
    );
    assert.ok(html.includes(t("auditCatalog")));
    assert.ok(html.includes(`<code>${identifier}</code>`));
    assert.ok(html.includes("admin-identifier"));
    assert.ok(html.includes("<table"));
    assert.ok(!html.includes("list-card"));
    const empty = renderToStaticMarkup(
      React.createElement(AdminAuditLog, {
        logs: [],
        locale,
        t,
        fallbackLabel: (action) => action,
      }),
    );
    assert.ok(empty.includes(t("adminNoAudit")));
  }
});

test("removed content disables file actions and revoked or expired shares have translated states", () => {
  const sample = {
    id: "item",
    userId: "usr_owner",
    pasteId: "pst_parent",
    fileName: "review.txt",
    pasteTitle: "Parent",
    pasteStatus: "taken_down",
    status: "active",
    scanStatus: "clean",
    sha256: "abc",
    size: 12,
    createdAt: "2026-10-19T10:00:00Z",
    expiresAt: "2020-01-01T00:00:00Z",
  };
  const render = (name, row, locale) => {
    const components = load({
      ...React,
      useEffect() {},
      useState(initial) {
        return [
          Array.isArray(initial)
            ? [row]
            : typeof initial === "boolean"
              ? false
              : initial,
          () => {},
        ];
      },
    });
    const t = (key) => components.adminCopy[locale][key] ?? key;
    return {
      t,
      html: renderToStaticMarkup(
        React.createElement(components[name], {
          locale,
          t,
          onChanged() {},
          statusLabel: (status) => status,
          scanDetail: () => ({
            label: "Clean",
            description: "",
            tone: "success",
          }),
        }),
      ),
    };
  };
  for (const locale of ["en", "zh-CN", "zh-TW", "es"]) {
    const file = render("AdminFileManager", sample, locale);
    assert.ok(file.html.includes(file.t("adminTakenDown")));
    assert.equal((file.html.match(/<button[^>]*disabled=""/g) ?? []).length, 5);
    const revoked = render(
      "AdminShareManager",
      { ...sample, revokedAt: "2026-10-19T10:00:00Z" },
      locale,
    );
    assert.ok(revoked.html.includes(revoked.t("adminShareRevoked")));
    assert.ok(!revoked.html.includes("stateRevoked"));
    const expired = render("AdminShareManager", sample, locale);
    assert.ok(expired.html.includes(expired.t("adminShareExpired")));
  }
});
