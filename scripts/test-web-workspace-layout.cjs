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
    fileName: "extracted.tsx",
    compilerOptions: { target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.React },
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

test("workspace introductions, overview state and their obsolete styles are removed", () => {
  assert.equal(
    nodesWhere(
      (node) =>
        ts.isVariableDeclaration(node) &&
        ["overviewOpen", "viewSummary", "viewSummaries"].some((name) =>
          node.name.getText(app).includes(name),
        ),
    ).length,
    0,
  );
  css.walkRules((rule) => {
    assert.ok(!/workspace-(?:overview|hero|stat)/.test(rule.selector));
  });
  assert.ok(!app.getText().includes('t("workspaceOverview")'));
});

test("four account metrics appear once inside the sidebar plan panel", () => {
  const usage = element("dl", "workspace-usage");
  assert.equal(
    nodesWhere(
      (node) =>
        ts.isJsxOpeningElement(node) &&
        jsxAttribute(node, "className")?.text === "workspace-usage",
    ).length,
    1,
  );
  const quota = element("section", "quota-panel").parent;
  assert.ok(quota.getText(app).includes(usage.getText(app)));
  assert.ok(quota.getText(app).includes("workspaceStats.map"));
  assert.ok(!quota.getText(app).includes("formatBytes(storageUsed)"));
  let parent = quota.parent;
  while (parent && !ts.isJsxElement(parent)) parent = parent.parent;
  assert.equal(
    jsxAttribute(parent.openingElement, "id").text,
    "workspace-menu",
  );
  const stats = evaluate(
    initializer("workspaceStats").arguments[0].getText(app),
    {
      quota: { activePasteCount: 9 },
      pastes: [{ id: "one" }],
      activePlan: { activePasteLimit: 20 },
      storageUsed: 256,
      storagePercent: 25,
      storageLimit: 1024,
      shares: [{ id: "share" }],
      sharedPasteCount: 1,
      attachmentCount: 3,
      expiringCount: 2,
      formatBytes: (bytes) => `${bytes} B`,
      t: (key) => key,
    },
  )();
  assert.equal(stats.length, 4);
  assert.equal(
    stats[0].value,
    "9",
    "quota stays authoritative, not the filtered list",
  );
  assert.equal(stats[1].value, "256 B");
  assert.equal(stats[2].value, "1");
  assert.equal(stats[3].value, "3");
  assert.equal(stats[3].detail, "2 expiringIn24h");
});

test("sidebar usage stays compact, flexible and hidden with the desktop rail", () => {
  for (const width of [320, 375, 390, 680, 980, 981, 1440]) {
    const grid = declarationsFor(".workspace-usage", width);
    assert.equal(grid["grid-template-columns"], "repeat(2, minmax(0, 1fr))");
    assert.ok(parseFloat(grid.gap) <= 8);
    const metric = declarationsFor(".usage-stat", width);
    assert.equal(metric["min-width"], "0");
    assert.equal(metric["overflow-wrap"], "anywhere");
    assert.equal(metric.height, undefined, "translated details can wrap");
  }
  for (const width of [981, 1440]) {
    assert.equal(
      declarationsFor(".app-shell--sidebar-collapsed .quota-panel", width)
        .display,
      "none",
    );
  }
});

test("mobile menu starts closed and navigation closes it without discarding drafts", () => {
  const state = initializer("[mobileMenuOpen, setMobileMenuOpen]");
  assert.equal(state.arguments[0].kind, ts.SyntaxKind.FalseKeyword);
  const [navigate] = nodesWhere(
    (node) =>
      ts.isFunctionDeclaration(node) && node.name?.text === "navigateWorkspace",
  );
  const calls = [];
  evaluate(navigate.getText(app), {
    setView: (view) => calls.push(["view", view]),
    setMobileMenuOpen: (value) => calls.push(["menu", value]),
    mobileMenuOpen: true,
    mobileMenuButtonRef: { current: { focus: () => calls.push(["focus"]) } },
  })("sends");
  assert.deepEqual(calls, [["view", "sends"], ["menu", false], ["focus"]]);
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
    for (const selector of [".workspace-menu", ".topbar"]) {
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

test("mobile menu exposes only existing regions with stable accessible controls", () => {
  const toggle = element("button", "mobile-menu-toggle");
  assert.equal(
    jsxAttribute(toggle, "aria-expanded").expression.getText(app),
    "mobileMenuOpen",
  );
  assert.equal(
    jsxAttribute(toggle, "aria-controls").text,
    "workspace-menu workspace-tools",
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
  const progress = element("div", "quota-bar");
  assert.equal(jsxAttribute(progress, "role").text, "progressbar");
  assert.equal(
    jsxAttribute(progress, "aria-valuenow").expression.getText(app),
    "storagePercent",
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

test("session changes clear menu and editor selection", () => {
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
  })();
  assert.deepEqual(calls, [
    ["selected", ""],
    ["menu", false],
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

function functionFromSource(name, context) {
  const [node] = nodesWhere(
    (node) => ts.isFunctionDeclaration(node) && node.name?.text === name,
  );
  assert.ok(node, `${name} must exist`);
  return evaluate(node.getText(app), context);
}

const baseCopy = evaluate(initializer("baseCopy").getText(app));
const translations = evaluate(initializer("copy").getText(app), { baseCopy });
const scanDetail = functionFromSource("attachmentScanDetail", {
  attachmentScanText: evaluate(initializer("attachmentScanText").getText(app)),
  isChineseLocale: (locale) => locale.startsWith("zh"),
});

function renderPasteCard(paste, locale, selectedId = "") {
  const React = require("../web/node_modules/react");
  const {
    renderToStaticMarkup,
  } = require("../web/node_modules/react-dom/server");
  const icons = Object.fromEntries(
    [
      "FileText",
      "ImageIcon",
      "FileUp",
      "Clock3",
      "Pin",
      "Star",
      "ClipboardCopy",
      "TimerReset",
      "Trash2",
      "Tags",
    ].map((name) => [
      name,
      () => React.createElement("svg", { "data-icon": name }),
    ]),
  );
  const List = functionFromSource("PasteList", {
    React,
    ...icons,
    copyFor: (language) => (key) =>
      translations[language][key] ?? translations.en[key] ?? key,
    attachmentScanDetail: scanDetail,
    formatBytes: (value) => `${value} B`,
    formatDuration: (value) => `${value}s`,
    pasteFileSummary: (attachments) =>
      attachments.map((item) => item.fileName).join(", "),
    attachmentDownloadPath: (id) => `/files/${id}`,
    AttachmentDownloadItem: ({ attachment }) =>
      React.createElement("a", {}, attachment.fileName),
  });
  const noop = () => {};
  return renderToStaticMarkup(
    React.createElement(List, {
      pastes: [paste],
      locale,
      selectedId,
      activeTag: "",
      onSelect: noop,
      onCopy: noop,
      onDelete: noop,
      onExtend: noop,
      onToggleFlag: noop,
      onFilterTag: noop,
    }),
  );
}

const cardFixture = {
  id: "one",
  title: "A long content title",
  text: "Saved content",
  textPreview: "Saved preview",
  tags: ["sample"],
  pinned: false,
  favorite: false,
  shareCount: 1,
  sizeBytes: 128,
  secondsToLive: 3600,
  scanStatus: "clean",
  attachments: [],
};

test("content cards render localized titles and scan labels in all four locales", () => {
  for (const [locale, title, label] of [
    ["en", "My content", "Clean"],
    ["zh-CN", "我的内容", "扫描通过"],
    ["zh-TW", "我的內容", "掃描通過"],
    ["es", "Mi contenido", "Limpio"],
  ]) {
    const markup = renderPasteCard(cardFixture, locale, "one");
    assert.ok(markup.includes(`<h2>${title}</h2>`));
    assert.ok(markup.includes(`>${label}</span>`));
    assert.match(markup, /class="paste-main"[^>]*aria-pressed="true"/);
    assert.match(markup, /data-icon="FileText"/);
    assert.ok(markup.includes("Saved preview"));
    assert.ok(markup.includes("128 B"));
    assert.ok(markup.includes("3600s"));
    assert.equal((markup.match(/class="card-actions"/g) ?? []).length, 1);
    assert.ok(markup.includes(translations[locale].deletePaste));
  }
});

test("image and grouped-file cards retain type icons, named files and explicit selection", () => {
  for (const [contentType, icon] of [
    ["image/png", "ImageIcon"],
    ["application/pdf", "FileUp"],
  ]) {
    const markup = renderPasteCard(
      {
        ...cardFixture,
        text: "",
        textPreview: "",
        attachments: [
          { id: "a", fileName: "one.ext", contentType },
          { id: "b", fileName: "two.ext", contentType },
        ],
      },
      "zh-CN",
    );
    assert.match(markup, new RegExp(`data-icon="${icon}"`));
    assert.ok(markup.includes("one.ext, two.ext"));
    assert.match(markup, /class="paste-main"[^>]*aria-pressed="false"/);
  }
});

test("localized scan presentation preserves owner and public download restrictions", () => {
  for (const locale of ["en", "zh-CN", "zh-TW", "es"]) {
    for (const [status, tone, owner, shared] of [
      ["clean", "success", true, true],
      ["pending", "warning", true, false],
      ["scan_failed", "warning", true, false],
      ["malicious", "danger", false, false],
      ["unknown_status", "neutral", true, false],
    ]) {
      const own = scanDetail({ scanStatus: status }, locale, "owner");
      const pub = scanDetail(
        { scanStatus: status.toUpperCase() },
        locale,
        "public",
      );
      assert.equal(own.tone, tone);
      assert.equal(pub.tone, tone);
      assert.equal(own.canDownload, owner);
      assert.equal(pub.canDownload, shared);
      if (status !== "unknown_status")
        assert.ok(own.label.length > 0 && pub.label.length > 0);
    }
  }
});

test("card selection surfaces are borderless with keyboard focus and lightweight touch actions", () => {
  for (const width of [320, 375, 390, 680, 981, 1440]) {
    const main = declarationsFor(".paste-card .paste-main", width);
    assert.equal(main.border, "0");
    assert.equal(main.background, "transparent");
    assert.equal(main["box-shadow"], "none");
    assert.equal(main["min-width"], "0");
    assert.match(
      declarationsFor(".paste-card .paste-main:focus-visible", width).outline,
      /^2px/,
    );
    const action = declarationsFor(
      ".paste-card .card-actions .icon-button",
      width,
    );
    assert.equal(action.background, "transparent");
    assert.equal(action["border-color"], "transparent");
    assert.ok(parseFloat(action["min-height"]) >= 44);
    assert.equal(
      declarationsFor(".paste-card .paste-preview", width)[
        "-webkit-line-clamp"
      ],
      "2",
    );
    assert.equal(
      declarationsFor(".paste-content", width)["overflow-wrap"],
      "anywhere",
    );
    assert.equal(
      declarationsFor(".paste-card.selected", width)["border-color"],
      "var(--brand-teal)",
    );
  }
});

test("scan descriptions keep localized risk details when present", () => {
  for (const [locale, prefix] of [
    ["en", "Risk"],
    ["zh-CN", "风险"],
    ["zh-TW", "風險"],
    ["es", "Riesgo"],
  ]) {
    const detail = scanDetail(
      { scanStatus: "scan_failed", risk: "  reason-code  " },
      locale,
      "owner",
    );
    assert.ok(detail.description.includes(`${prefix}: reason-code`));
    assert.equal(detail.tone, "warning");
    assert.equal(detail.canDownload, true);
  }
});
