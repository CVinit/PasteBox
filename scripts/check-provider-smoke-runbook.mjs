#!/usr/bin/env node
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = new URL("..", import.meta.url).pathname;
const runbookPath = join(repoRoot, "docs/production-provider-smoke-tests.md");
const checklistPath = join(repoRoot, "docs/production-launch-evidence-checklist.md");
const releaseTemplatePath = join(repoRoot, "docs/production-release-notes-template.md");
const deploymentRunbookPath = join(repoRoot, "docs/production-deployment-runbook.md");

function fail(message) {
  console.error(`provider smoke runbook check failed: ${message}`);
  process.exit(1);
}

function readRequired(path) {
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

const runbook = readRequired(runbookPath);
const checklist = readRequired(checklistPath);
const releaseTemplate = readRequired(releaseTemplatePath);
const deploymentRunbook = readRequired(deploymentRunbookPath);

const requiredRunbookSections = [
  "## Managed S3-Compatible Object Storage",
  "## SMTP Delivery",
  "## Google OAuth",
  "## Stripe",
  "## Epusdt",
  "## ClamAV",
  "## Completion",
];

for (const section of requiredRunbookSections) {
  requireIncludes("provider smoke runbook", runbook, section);
}

const requiredEvidence = [
  "upload, private read, and delete",
  "Verify your PasteBox email",
  "Your PasteBox registration code",
  "Reset your PasteBox password",
  "New PasteBox login",
  "PasteBox payment received",
  "PasteBox account deletion requested",
  "authError=invalid_google_state",
  "does not contain `/dev/checkout`",
  "double-activated",
  "refunded` or `canceled`",
  "plain `ok`",
  "`expired` or `canceled`",
  "EICAR test string",
  "blocked for the malicious attachment",
];

for (const evidence of requiredEvidence) {
  requireIncludes("provider smoke runbook", runbook, evidence);
}

const runbookReference = "docs/production-provider-smoke-tests.md";
requireIncludes("production launch evidence checklist", checklist, runbookReference);
requireIncludes("production release notes template", releaseTemplate, runbookReference);
requireIncludes("production deployment runbook", deploymentRunbook, runbookReference);

console.log("provider smoke runbook check passed");
