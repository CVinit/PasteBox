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
function loadSelect(react = React) {
  const exports = {};
  const source = fs.readFileSync(
    require("node:path").join(__dirname, "../web/src/AdminSelect.tsx"),
    "utf8",
  );
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
      jsx: ts.JsxEmit.ReactJSX,
    },
  }).outputText;
  vm.runInNewContext(compiled, {
    exports,
    document: { body: {} },
    require: (name) => {
      if (name === "react") return react;
      if (name === "react-dom") return { createPortal: (node) => node };
      if (name === "react/jsx-runtime")
        return require("../web/node_modules/react/jsx-runtime");
      if (name === "lucide-react")
        return require("../web/node_modules/lucide-react");
      throw new Error(`Unexpected select import: ${name}`);
    },
  });
  return exports.AdminSelect;
}
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
      if (name === "./AdminSelect") return { AdminSelect: loadSelect(react) };
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

// Exercise the real select's event handlers; positioning and browser focus are
// checked against the live admin preview, not a simulated layout engine.
function selectControl(overrides = {}) {
  const state = [];
  let cursor = 0;
  const changes = [];
  const props = {
    label: "Log level",
    value: "info",
    options: [
      { value: "debug", label: "Debug" },
      { value: "info", label: "Information" },
      { value: "trace", label: "Trace", disabled: true },
      { value: "warn", label: "Warning" },
      { value: "error", label: "Error" },
    ],
    onChange(value) {
      changes.push(value);
      props.value = value;
    },
    ...overrides,
  };
  const component = loadSelect({
    useId: () => "select-test",
    useEffect() {},
    useLayoutEffect() {},
    useRef(initial) {
      const index = cursor++;
      if (!(index in state)) state[index] = { current: initial };
      return state[index];
    },
    useState(initial) {
      const index = cursor++;
      if (!(index in state)) state[index] = initial;
      return [
        state[index],
        (next) => {
          state[index] = next;
        },
      ];
    },
  });
  function render() {
    cursor = 0;
    const [trigger, menu] = component(props).props.children;
    return { trigger: trigger.props, menu: menu?.props };
  }
  return {
    changes,
    render,
    key(key) {
      render().trigger.onKeyDown({
        key,
        preventDefault() {},
        stopPropagation() {},
      });
      return render();
    },
  };
}

test("admin select uses keyboard navigation, skips disabled options and commits only on confirmation", () => {
  const select = selectControl();
  let view = select.key("ArrowDown");
  assert.equal(view.trigger["aria-expanded"], true);
  assert.equal(view.trigger["aria-activedescendant"], "select-test-1");
  view = select.key("ArrowDown");
  assert.equal(view.trigger["aria-activedescendant"], "select-test-3");
  assert.equal(select.changes.length, 0);
  select.key("Home");
  assert.equal(
    select.render().trigger["aria-activedescendant"],
    "select-test-0",
  );
  select.key("End");
  view = select.key("Enter");
  assert.deepEqual(select.changes, ["error"]);
  assert.equal(view.trigger["aria-expanded"], false);
  select.key(" ");
  select.key("Home");
  select.key("Escape");
  assert.deepEqual(select.changes, ["error"]);
  assert.equal(select.render().trigger["aria-expanded"], false);
  select.key("ArrowUp");
  assert.equal(select.key("Tab").trigger["aria-expanded"], false);
});

test("admin select typeahead and pointer selection preserve value and selected state", () => {
  const select = selectControl();
  let view = select.key("w");
  assert.equal(view.trigger["aria-activedescendant"], "select-test-3");
  assert.equal(view.menu.children[1].props["aria-selected"], true);
  select.key("Enter");
  assert.deepEqual(select.changes, ["warn"]);
  select.render().trigger.onClick();
  view = select.render();
  view.menu.children[2].props.onClick();
  assert.deepEqual(select.changes, ["warn"]);
  view.menu.children[0].props.onClick();
  assert.deepEqual(select.changes, ["warn", "debug"]);
  assert.equal(select.render().trigger["aria-expanded"], false);
});

test("empty and disabled admin selects cannot open or change values", () => {
  for (const overrides of [
    { disabled: true },
    { options: [] },
    { options: [{ value: "info", label: "Info", disabled: true }] },
  ]) {
    const select = selectControl(overrides);
    assert.equal(select.render().trigger.disabled, true);
    select.render().trigger.onClick();
    select.key("ArrowDown");
    assert.equal(select.render().trigger["aria-expanded"], false);
    assert.deepEqual(select.changes, []);
  }
});
