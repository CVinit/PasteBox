// Guard the responsive rules against the actual JSX and CSS, without a browser.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const ts = require("../web/node_modules/typescript");
const postcss = require("../web/node_modules/postcss");

const root = path.resolve(__dirname, "..");
const app = ts.createSourceFile(
  "App.tsx",
  fs.readFileSync(path.join(root, "web/src/App.tsx"), "utf8"),
  ts.ScriptTarget.Latest,
  true,
  ts.ScriptKind.TSX,
);
const css = postcss.parse(
  fs.readFileSync(path.join(root, "web/src/styles.css"), "utf8"),
);

function declarationsFor(selector, width) {
  const result = {};
  css.walkRules((rule) => {
    if (!rule.selectors.includes(selector)) return;
    for (let parent = rule.parent; parent; parent = parent.parent) {
      if (parent.type !== "atrule" || parent.name !== "media") continue;
      const maximum = parent.params.match(/max-width:\s*(\d+)px/);
      const minimum = parent.params.match(/min-width:\s*(\d+)px/);
      if (maximum && width > Number(maximum[1])) return;
      if (minimum && width < Number(minimum[1])) return;
    }
    rule.walkDecls((declaration) => {
      result[declaration.prop] = declaration.value;
    });
  });
  return result;
}

function overviewClass(view) {
  let expression;
  function visit(node) {
    if (
      ts.isJsxOpeningElement(node) &&
      node.tagName.getText(app) === "section"
    ) {
      const attributes = node.attributes.properties.filter(ts.isJsxAttribute);
      if (
        attributes.some(
          (attribute) =>
            attribute.name.getText(app) === "aria-label" &&
            attribute.initializer?.expression?.getText(app) ===
              't("workspaceOverview")',
        )
      ) {
        expression = attributes
          .find((attribute) => attribute.name.getText(app) === "className")
          ?.initializer?.expression?.getText(app);
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(app);
  assert.ok(expression, "Workspace overview needs a view-scoped class");
  return vm.runInNewContext(expression, { view });
}

function nodesWhere(predicate) {
  const result = [];
  function visit(node) {
    if (predicate(node)) result.push(node);
    ts.forEachChild(node, visit);
  }
  visit(app);
  return result;
}

function initializer(name) {
  const [node] = nodesWhere(
    (node) => ts.isVariableDeclaration(node) && node.name.getText(app) === name,
  );
  assert.ok(node?.initializer, `${name} must exist in the production source`);
  return node.initializer;
}

function jsxAttribute(node, name) {
  return node.attributes.properties.find(
    (attribute) =>
      ts.isJsxAttribute(attribute) && attribute.name.getText(app) === name,
  )?.initializer;
}

function element(tag, className) {
  const [node] = nodesWhere(
    (node) =>
      (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) &&
      node.tagName.getText(app) === tag &&
      jsxAttribute(node, "className")?.text === className,
  );
  assert.ok(node, `${tag}.${className} must exist`);
  return node;
}

function evaluate(code, context = {}) {
  const compiled = ts.transpileModule(`(${code})`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022 },
  }).outputText;
  return vm.runInNewContext(compiled, context);
}

test("ordinary status messages retain their surface and gain readable spacing", () => {
  for (const width of [375, 390, 1440]) {
    const style = declarationsFor(".status-line", width);
    assert.equal(style.border, "1px solid var(--line)");
    assert.equal(style.background, "var(--surface-card, #f5f0e0)");
    assert.equal(style["border-radius"], "var(--radius-md)");
    assert.ok(parseFloat(style.padding) >= 12);
    assert.ok(Number(style["line-height"]) >= 1.4);
    assert.equal(style["overflow-wrap"], "anywhere");
  }
});

test("only the compose view opts into the compact overview", () => {
  assert.equal(
    overviewClass("inbox"),
    "workspace-hero workspace-hero--compose",
  );
  for (const view of ["sends", "shared", "billing", "settings", "admin"]) {
    assert.equal(overviewClass(view), "workspace-hero");
  }
});

test("the compact overview uses two columns only at phone widths", () => {
  const scope = ".workspace-hero--compose";
  for (const width of [320, 375, 390, 680]) {
    assert.equal(
      declarationsFor(`${scope} .workspace-hero-copy`, width).display,
      "none",
    );
    const grid = declarationsFor(`${scope} .workspace-stat-grid`, width);
    assert.equal(grid["grid-template-columns"], "repeat(2, minmax(0, 1fr))");
    assert.ok(parseFloat(grid.gap) <= 8);
    const card = declarationsFor(`${scope} .workspace-stat`, width);
    assert.ok(parseFloat(card["min-height"]) <= 88);
    assert.ok(parseFloat(card.padding) <= 12);
    assert.equal(card.height, undefined, "Long labels must be able to wrap");
  }
  for (const width of [681, 980, 1440]) {
    assert.deepEqual(
      declarationsFor(`${scope} .workspace-hero-copy`, width),
      {},
    );
    assert.deepEqual(
      declarationsFor(`${scope} .workspace-stat-grid`, width),
      {},
    );
    assert.deepEqual(declarationsFor(`${scope} .workspace-stat`, width), {});
  }
});

test("menus and overviews start closed and navigation closes both without discarding drafts", () => {
  for (const name of ["mobileMenuOpen", "overviewOpen"]) {
    const state = initializer(
      `[${name}, set${name[0].toUpperCase()}${name.slice(1)}]`,
    );
    assert.equal(state.arguments[0].kind, ts.SyntaxKind.FalseKeyword);
  }
  const [navigate] = nodesWhere(
    (node) =>
      ts.isFunctionDeclaration(node) && node.name?.text === "navigateWorkspace",
  );
  const calls = [];
  evaluate(navigate.getText(app), {
    setView: (view) => calls.push(["view", view]),
    setOverviewOpen: (value) => calls.push(["overview", value]),
    setMobileMenuOpen: (value) => calls.push(["menu", value]),
    mobileMenuOpen: true,
    mobileMenuButtonRef: { current: { focus: () => calls.push(["focus"]) } },
  })("sends");
  assert.deepEqual(calls, [
    ["view", "sends"],
    ["overview", false],
    ["menu", false],
    ["focus"],
  ]);
});

test("desktop sidebar preference is restored, including a blocked storage fallback", () => {
  const state = initializer("[sidebarCollapsed, setSidebarCollapsed]");
  const restore = state.arguments[0].getText(app);
  assert.equal(
    evaluate(restore, {
      window: { localStorage: { getItem: () => "true" } },
    })(),
    true,
  );
  assert.equal(
    evaluate(restore, { window: { localStorage: { getItem: () => null } } })(),
    false,
  );
  assert.equal(
    evaluate(restore, {
      window: {
        get localStorage() {
          throw Error("blocked");
        },
      },
    })(),
    false,
  );
});

test("mobile hiding is scoped to chrome while collapsed desktop keeps an icon rail", () => {
  for (const width of [320, 375, 390, 680, 980]) {
    for (const selector of [
      ".workspace-menu",
      ".topbar",
      ".workspace-overview",
    ]) {
      assert.equal(
        declarationsFor(
          `.app-shell:not(.app-shell--menu-open) ${selector}`,
          width,
        ).display,
        "none",
      );
    }
    assert.equal(
      declarationsFor(".mobile-menu-toggle", width).display,
      "inline-grid",
    );
    assert.equal(declarationsFor(".sidebar-toggle", width).display, "none");
    assert.equal(
      declarationsFor(".app-shell--sidebar-collapsed", width)[
        "grid-template-columns"
      ],
      undefined,
    );
  }
  for (const width of [981, 1440]) {
    assert.equal(
      declarationsFor(".app-shell--sidebar-collapsed", width)[
        "grid-template-columns"
      ],
      "84px minmax(0, 1fr)",
    );
    assert.equal(declarationsFor(".mobile-menu-toggle", width).display, "none");
    assert.equal(
      declarationsFor(".app-shell:not(.app-shell--menu-open) .topbar", width)
        .display,
      undefined,
    );
  }
});

test("native overview disclosure and mobile menu expose stable accessible controls", () => {
  const overview = element("details", "workspace-overview");
  assert.equal(
    jsxAttribute(overview, "open").expression.getText(app),
    "overviewOpen",
  );
  assert.ok(jsxAttribute(overview, "onToggle"));
  const toggle = element("button", "mobile-menu-toggle");
  assert.equal(
    jsxAttribute(toggle, "aria-expanded").expression.getText(app),
    "mobileMenuOpen",
  );
  for (const id of jsxAttribute(toggle, "aria-controls").text.split(" ")) {
    assert.ok(
      nodesWhere(
        (node) =>
          ts.isJsxOpeningElement(node) && jsxAttribute(node, "id")?.text === id,
      ).length,
      `controlled region ${id} must exist`,
    );
  }
  assert.equal(
    declarationsFor(".workspace-overview:not([open]) > .workspace-hero", 1440)
      .display,
    "none",
  );
});

test("title, text and file staging precede optional tags and send settings", () => {
  const composer = element("section", "composer").parent;
  const body = composer.getText(app);
  for (const entry of [
    'className="title-input"',
    "<textarea",
    "className={`drop-zone",
  ]) {
    const index = body.indexOf(entry);
    assert.ok(index >= 0, `${entry} must be present`);
    assert.ok(index < body.indexOf('className="composer-controls"'));
    assert.ok(index < body.indexOf("<SendSettingsFields"));
  }
});

test("selected content never falls back to the first record", () => {
  const lookup = initializer("selectedPaste").arguments[0].getText(app);
  const pastes = [{ id: "one" }, { id: "two" }];
  for (const selectedPasteId of ["", "missing"]) {
    assert.equal(evaluate(lookup, { pastes, selectedPasteId })(), undefined);
  }
  assert.equal(
    evaluate(lookup, { pastes, selectedPasteId: "two" })(),
    pastes[1],
  );
  assert.equal(
    evaluate(lookup, { pastes: [], selectedPasteId: "two" })(),
    undefined,
  );
});

test("refreshing a populated list does not select it or overwrite an explicit choice", async () => {
  const callback = initializer("refreshAuthed").arguments[0].getText(app);
  for (const selectedPasteId of ["", "two"]) {
    let records;
    await evaluate(callback, {
      query: "",
      filter: "all",
      tagFilter: "",
      selectedPasteId,
      searchParams: () => ({}),
      client: {
        quota: async () => ({}),
        pastes: async () => ({ pastes: [{ id: "one" }, { id: "two" }] }),
        shares: async () => ({ shares: [] }),
        orders: async () => ({ orders: [] }),
      },
      setQuota: () => {},
      setShares: () => {},
      setOrders: () => {},
      setPastes: (value) => {
        records = value;
      },
      setSelectedPasteId: () =>
        assert.fail("refresh must not change selection"),
    })();
    assert.equal(records.length, 2);
  }
});

test("detail panes are conditional and a list without selection has one full-width column", () => {
  const panel = element("aside", "side-panel");
  const condition = panel.parent.parent.parent;
  assert.ok(ts.isConditionalExpression(condition));
  assert.equal(condition.condition.getText(app), "selectedPaste");
  assert.equal(condition.whenFalse.kind, ts.SyntaxKind.NullKeyword);
  for (const width of [375, 980, 1440]) {
    assert.equal(
      declarationsFor(".content-grid--list-only", width)[
        "grid-template-columns"
      ],
      "minmax(0, 1fr)",
    );
  }
  const list = nodesWhere(
    (node) =>
      ts.isJsxSelfClosingElement(node) &&
      node.tagName.getText(app) === "PasteList",
  )[0];
  assert.equal(
    jsxAttribute(list, "onSelect").expression.getText(app),
    "setSelectedPasteId",
  );
  const setters = nodesWhere(
    (node) =>
      ts.isCallExpression(node) &&
      node.expression.getText(app) === "setSelectedPasteId",
  );
  assert.equal(
    setters.length,
    3,
    "only session changes, deletion and explicit close may clear selection; creation/publishing must not select",
  );
});

test("deleting the selected record clears only that choice, and failed deletes keep it", async () => {
  const list = nodesWhere(
    (node) =>
      ts.isJsxSelfClosingElement(node) &&
      node.tagName.getText(app) === "PasteList",
  )[0];
  const callback = jsxAttribute(list, "onDelete").expression.getText(app);
  for (const [selected, deleted, result, expected] of [
    ["one", "one", undefined, ""],
    ["two", "one", undefined, "two"],
    ["one", "one", null, "one"],
  ]) {
    let selection = selected;
    let refreshes = 0;
    await evaluate(callback, {
      client: { deletePaste: () => {} },
      t: (key) => key,
      run: async () => result,
      setSelectedPasteId: (next) => {
        selection = next(selection);
      },
      refreshAuthed: async () => {
        refreshes += 1;
      },
    })(deleted);
    assert.equal(selection, expected);
    assert.equal(refreshes, result === null ? 0 : 1);
  }
});

test("important feedback remains outside foldable workspace controls", () => {
  const feedback = element("p", "status-line workspace-feedback");
  assert.equal(jsxAttribute(feedback, "role").text, "status");
  for (let parent = feedback.parent; parent; parent = parent.parent) {
    if (ts.isJsxElement(parent)) {
      assert.notEqual(parent.openingElement.tagName.getText(app), "header");
      assert.notEqual(parent.openingElement.tagName.getText(app), "details");
    }
  }
});

test("session changes clear menu, overview and editor selection", () => {
  const effect = nodesWhere(
    (node) =>
      ts.isCallExpression(node) &&
      node.expression.getText(app) === "useEffect" &&
      node.arguments[1]?.getText(app) === "[user?.id]",
  )[0];
  assert.ok(effect, "a new session must start with closed workspace chrome");
  const calls = [];
  evaluate(effect.arguments[0].getText(app), {
    setSelectedPasteId: (value) => calls.push(["selected", value]),
    setMobileMenuOpen: (value) => calls.push(["menu", value]),
    setOverviewOpen: (value) => calls.push(["overview", value]),
  })();
  assert.deepEqual(calls, [
    ["selected", ""],
    ["menu", false],
    ["overview", false],
  ]);
});

test("attachment-to-selected action is hidden until a record has been clicked", () => {
  const trigger = nodesWhere(
    (node) =>
      ts.isJsxOpeningElement(node) &&
      node.tagName.getText(app) === "button" &&
      jsxAttribute(node, "onClick")
        ?.expression?.getText(app)
        .includes("document.getElementById(attachInputId)"),
  )[0];
  assert.ok(trigger);
  const condition = trigger.parent.parent.parent.parent;
  assert.ok(ts.isConditionalExpression(condition));
  assert.equal(
    condition.condition.getText(app),
    'sendMode === "file" && selectedPaste',
  );
});
