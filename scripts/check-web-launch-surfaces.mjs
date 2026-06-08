#!/usr/bin/env node
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = new URL("..", import.meta.url).pathname;
const appSourcePath = join(repoRoot, "web/src/App.tsx");
const apiSourcePath = join(repoRoot, "web/src/api.ts");
const distPath = join(repoRoot, "web/dist");
const distAssetsPath = join(distPath, "assets");
const distIndexPath = join(distPath, "index.html");

function fail(message) {
  console.error(`web launch surface check failed: ${message}`);
  process.exit(1);
}

function requireFile(path) {
  if (!existsSync(path)) {
    fail(`missing required file ${path}`);
  }
  return readFileSync(path, "utf8");
}

function requireIncludes(label, content, expected) {
  if (!content.includes(expected)) {
    fail(`${label} is missing ${JSON.stringify(expected)}`);
  }
}

function requireMatches(label, content, pattern) {
  if (!pattern.test(content)) {
    fail(`${label} does not match ${pattern}`);
  }
}

const appSource = requireFile(appSourcePath);
const apiSource = requireFile(apiSourcePath);
const distIndex = requireFile(distIndexPath);

if (!existsSync(distAssetsPath)) {
  fail(`missing built asset directory ${distAssetsPath}`);
}

const jsAssets = readdirSync(distAssetsPath)
  .filter((name) => name.endsWith(".js"))
  .map((name) => requireFile(join(distAssetsPath, name)));

if (jsAssets.length === 0) {
  fail("web/dist/assets does not contain a JavaScript bundle");
}

const bundle = jsAssets.join("\n");

const publicRoutes = [
  "/legal",
  "/legal/terms",
  "/legal/privacy",
  "/legal/refund",
  "/legal/abuse",
  "/legal/cookies",
  "/legal/account-deletion",
  "/legal/data-export",
  "/legal/data-retention",
  "/legal/subprocessors",
  "/support",
  "/status",
];

for (const route of publicRoutes) {
  requireIncludes("web/src/App.tsx public page routes", appSource, `path: "${route}"`);
  requireIncludes("production JS bundle public page routes", bundle, route);
}

const publicPageTitles = [
  "PasteBox Legal And Support Hub",
  "Terms Of Service",
  "Privacy Policy",
  "Refund Policy",
  "Abuse And DMCA Policy",
  "Cookie Notice",
  "Account Deletion Instructions",
  "Data Export Instructions",
  "Data Retention Matrix",
  "Subprocessors",
  "Support Contact",
  "Status And Announcements",
];

for (const title of publicPageTitles) {
  requireIncludes("web/src/App.tsx public page content", appSource, title);
  requireIncludes("production JS bundle public page content", bundle, title);
}

const launchSurfaceCopy = [
  "support intake",
  "audit logs",
  "order ID",
  "Stripe refunds and cancellations",
  "Epusdt fixed-duration orders",
  "in-app export",
  "Delete request",
];

for (const copy of launchSurfaceCopy) {
  requireIncludes("web/src/App.tsx launch surface copy", appSource, copy);
  requireIncludes("production JS bundle launch surface copy", bundle, copy);
}

const criticalLinks = [
  'href="/legal/refund"',
  'href="/support"',
  'href="/legal/data-export"',
  'href="/legal/account-deletion"',
  'href="/legal/privacy"',
];

for (const link of criticalLinks) {
  requireIncludes("web/src/App.tsx product links", appSource, link);
}

requireIncludes("web/src/api.ts support contact client", apiSource, 'supportContacts: () => api<SupportContacts>("/support/contacts")');
requireIncludes("web/src/App.tsx support contacts load", appSource, "client.supportContacts()");
requireIncludes("web/src/App.tsx supported locale type", appSource, 'type Locale = "en" | "zh-CN" | "zh-TW" | "es"');
for (const locale of ['value: "zh-CN"', 'value: "zh-TW"', 'value: "es"']) {
  requireIncludes("web/src/App.tsx locale selector option", appSource, locale);
}
requireIncludes("web/src/App.tsx registration language payload", appSource, "language: locale");
requireIncludes("web/src/App.tsx registration email code payload", appSource, "emailVerificationCode: auth.emailVerificationCode");
for (const copy of ["简体中文", "繁體中文", "Español", "Crea una entrega segura."]) {
  requireIncludes("production JS bundle multilingual copy", bundle, copy);
}
requireIncludes("web/src/App.tsx public share route priority", appSource, "if (publicShareToken)");
if (appSource.includes("if (!user && publicShareToken)")) {
  fail("web/src/App.tsx only renders public share routes for anonymous users");
}
requireMatches("web/src/App.tsx support contact mailto rendering", appSource, /mailto:\$\{contacts\.supportEmail\}/);
requireMatches("web/src/App.tsx abuse contact mailto rendering", appSource, /mailto:\$\{contacts\.abuseEmail\}/);
requireIncludes("production index", distIndex, '<div id="root"></div>');

console.log("web launch surface check passed");
