#!/usr/bin/env node
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = new URL("..", import.meta.url).pathname;
const templatePath = join(repoRoot, "docs/production-release-notes-template.md");
const checklistPath = join(repoRoot, "docs/production-launch-evidence-checklist.md");
const runbookPath = join(repoRoot, "docs/production-deployment-runbook.md");
const validatorPath = join(repoRoot, "scripts/check-production-release-evidence.mjs");
const makefilePath = join(repoRoot, "Makefile");

function fail(message) {
  console.error(`release evidence template check failed: ${message}`);
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

const template = readRequired(templatePath);
const checklist = readRequired(checklistPath);
const runbook = readRequired(runbookPath);
readRequired(validatorPath);
const makefile = readRequired(makefilePath);

const requiredHeadings = [
  "## Release Identity",
  "## Repository Verification",
  "## Deployment Evidence",
  "## Provider Smoke Tests",
  "## Security And Browser Gates",
  "## Backup, PITR, And Rollback",
  "## Monitoring And Alerts",
  "## Legal, Support, And Product Operations",
  "## Known Residual Risks",
  "## Launch Decision",
];

for (const heading of requiredHeadings) {
  requireIncludes("production release notes template", template, heading);
}

const requiredEvidence = [
  "Immutable image reference or digest",
  "Migration classification",
  "make production-readiness",
  "Web launch-surface smoke result",
  "Release evidence validator self-test result",
  "Provider smoke-test runbook path",
  "Stripe signed webhook replay",
  "Epusdt expired or canceled callback",
  "Logical restore drill result and duration",
  "PITR restore drill result and duration",
  "Off-host restic snapshot ID",
  "Reversible image rollback rehearsal result",
  "Metrics scrape evidence",
  "Operator escalation targets",
  "Legal/support/status deep-link evidence",
  "Skipped checklist items with justification",
  "Release evidence validator result",
  "Public beta traffic accepted",
];

for (const evidence of requiredEvidence) {
  requireIncludes("production release notes template", template, evidence);
}

const templateReference = "docs/production-release-notes-template.md";
const validatorReference = "scripts/check-production-release-evidence.mjs";
const makeTargetReference = "make release-evidence";
requireIncludes("production launch evidence checklist", checklist, templateReference);
requireIncludes("production deployment runbook", runbook, templateReference);
requireIncludes("production launch evidence checklist", checklist, validatorReference);
requireIncludes("production launch evidence checklist", checklist, "node scripts/check-production-release-evidence.mjs --self-test");
requireIncludes("production release notes template", template, validatorReference);
requireIncludes("production deployment runbook", runbook, validatorReference);
requireIncludes("Makefile release evidence target", makefile, "release-evidence:");
requireIncludes("production launch evidence checklist", checklist, makeTargetReference);
requireIncludes("production release notes template", template, makeTargetReference);
requireIncludes("production deployment runbook", runbook, makeTargetReference);

console.log("release evidence template check passed");
