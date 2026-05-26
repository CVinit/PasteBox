#!/usr/bin/env node
import { existsSync, readFileSync } from "node:fs";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const checklistTemplatePath = join(repoRoot, "docs/production-launch-evidence-checklist.md");
const releaseNotesTemplatePath = join(repoRoot, "docs/production-release-notes-template.md");

const emptyTemplateValues = new Set([
  "",
  "no-migration / reversible / forward-compatible / non-reversible",
  "yes / no",
]);

const placeholderPattern =
  /\b(TBD|TODO|CHANGE_ME|REPLACE_ME|placeholder|example\.com|<[^>\n]+>)\b/i;

const forbiddenSecretPatterns = [
  ["Stripe secret key", /\b[rs]k_(?:live|test)_[A-Za-z0-9]{8,}\b/],
  ["Stripe webhook secret", /\bwhsec_[A-Za-z0-9]{8,}\b/],
  ["GitHub token", /\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b/],
  ["AWS access key", /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/],
  ["private key block", /-----BEGIN [A-Z ]*PRIVATE KEY-----/],
  ["bearer token", /\bBearer\s+[A-Za-z0-9._~+/=-]{20,}\b/],
  ["JWT", /\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b/],
];

const requiredChecklistHeadings = [
  "## Release Identity",
  "## Repository Verification",
  "## Production Environment",
  "## Deployment Evidence",
  "## Provider Smoke Tests",
  "## Security And Browser Gates",
  "## Backup, PITR, And Rollback",
  "## Monitoring And Alerts",
  "## Legal, Support, And Product Operations",
  "## Launch Decision",
];

const requiredReleaseHeadings = [
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

const requiredLaunchDecisionFields = [
  "Completed evidence checklist path",
  "Skipped checklist items with justification",
  "Release evidence validator result",
  "Operator approval",
  "Approval time",
  "Public beta traffic accepted",
];

const requiredChecklistFields = [
  "Release commit",
  "Immutable image reference or digest",
  "Production domain",
  "Deployment window",
  "Operator",
  "Previous known-good image",
  "Migration classification",
];

const releaseIdentityFields = [...requiredChecklistFields];
const allowedMigrationClassifications = new Set([
  "no-migration",
  "reversible",
  "forward-compatible",
  "non-reversible",
]);

let throwOnFailure = false;

function usage() {
  console.log(`Usage:
  node scripts/check-production-release-evidence.mjs --checklist <completed-checklist.md> --release-notes <completed-release-notes.md>
  node scripts/check-production-release-evidence.mjs --self-test

Validates sanitized operator-owned release evidence before public beta traffic.
The completed files should live in the operator evidence archive, not in the
repository when they contain live provider, domain, or deployment details.`);
}

function fail(message) {
  if (throwOnFailure) {
    throw new Error(message);
  }
  console.error(`production release evidence check failed: ${message}`);
  process.exit(1);
}

function parseArgs(argv) {
  const options = {};

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--help" || arg === "-h") {
      options.help = true;
      continue;
    }
    if (arg === "--self-test") {
      options.selfTest = true;
      continue;
    }
    if (arg === "--checklist" || arg === "--release-notes") {
      const value = argv[index + 1];
      if (!value || value.startsWith("--")) {
        fail(`${arg} requires a file path`);
      }
      options[arg.slice(2)] = value;
      index += 1;
      continue;
    }
    fail(`unknown argument ${arg}`);
  }

  return options;
}

function readRequired(path, label) {
  if (!path) {
    fail(`missing --${label} path`);
  }
  if (!existsSync(path)) {
    fail(`${label} file does not exist: ${path}`);
  }
  return readFileSync(path, "utf8");
}

function requireIncludes(label, content, expected) {
  if (!content.includes(expected)) {
    fail(`${label} is missing ${JSON.stringify(expected)}`);
  }
}

function checklistItems(markdown) {
  const items = [];
  let current = null;

  markdown.split(/\r?\n/).forEach((line, index) => {
    const match = line.match(/^- \[([ xX])\] (.*)$/);
    if (match) {
      if (current) {
        items.push(current);
      }
      current = {
        checked: match[1].toLowerCase() === "x",
        line,
        lineNumber: index + 1,
        text: match[2],
      };
      return;
    }
    if (current && /^  \S/.test(line)) {
      current.text += ` ${line.trim()}`;
    }
  });

  if (current) {
    items.push(current);
  }

  return items;
}

function normalizeFieldEntry(entry) {
  return entry
    .replace(/\s+/g, " ")
    .replace(/^- /, "")
    .trim();
}

function releaseFieldEntries(markdown) {
  const entries = [];
  let current = null;

  for (const rawLine of markdown.split(/\r?\n/)) {
    if (rawLine.startsWith("- ")) {
      if (current) {
        entries.push(current);
      }
      current = rawLine;
      continue;
    }
    if (current && /^  \S/.test(rawLine)) {
      current += ` ${rawLine.trim()}`;
    }
  }

  if (current) {
    entries.push(current);
  }

  return entries.map(normalizeFieldEntry).filter((entry) => entry.includes(":"));
}

function fieldName(entry) {
  return entry.slice(0, entry.indexOf(":")).trim();
}

function fieldValue(entry) {
  return entry.slice(entry.indexOf(":") + 1).trim();
}

function isPlaceholderValue(value) {
  return emptyTemplateValues.has(value) || placeholderPattern.test(value);
}

function validateNoRawSecrets(label, markdown) {
  const lines = markdown.split(/\r?\n/);
  lines.forEach((line, index) => {
    for (const [secretLabel, pattern] of forbiddenSecretPatterns) {
      if (pattern.test(line)) {
        fail(`${label} contains a raw ${secretLabel} at line ${index + 1}; store only sanitized evidence`);
      }
    }
  });
}

function normalizeChecklistText(text) {
  return text.replace(/\s+/g, " ").trim();
}

function checklistFieldValue(text, field) {
  const prefix = `${field}:`;
  if (!text.startsWith(prefix)) {
    return null;
  }
  return text.slice(prefix.length).trim();
}

function checklistItemSatisfied(templateText, completedTexts) {
  if (completedTexts.has(templateText)) {
    return true;
  }
  const requiredField = requiredChecklistFields.find((field) =>
    templateText.startsWith(`${field}:`),
  );
  if (requiredField) {
    return [...completedTexts].some((text) => text.startsWith(`${requiredField}:`));
  }
  if (templateText.endsWith(":")) {
    return [...completedTexts].some((text) => text.startsWith(templateText));
  }
  return false;
}

function checklistFieldEntries(markdown) {
  const values = new Map();
  for (const { text } of checklistItems(markdown)) {
    const normalized = normalizeChecklistText(text);
    for (const field of releaseIdentityFields) {
      const value = checklistFieldValue(normalized, field);
      if (value !== null) {
        values.set(field, value);
      }
    }
  }
  return values;
}

function releaseNotesFieldEntries(markdown) {
  return new Map(
    releaseFieldEntries(markdown).map((entry) => [fieldName(entry), fieldValue(entry)]),
  );
}

function normalizeEvidenceValue(value) {
  return value.replace(/\s+/g, " ").trim();
}

function validateMigrationClassification(label, value) {
  const normalized = normalizeEvidenceValue(value).toLowerCase();
  if (!allowedMigrationClassifications.has(normalized)) {
    fail(
      `${label} migration classification must be one of ${[...allowedMigrationClassifications].join(", ")}`,
    );
  }
}

function validateEvidenceConsistency(checklistMarkdown, releaseNotesMarkdown) {
  const checklistFields = checklistFieldEntries(checklistMarkdown);
  const releaseFields = releaseNotesFieldEntries(releaseNotesMarkdown);

  for (const field of releaseIdentityFields) {
    const checklistValue = checklistFields.get(field);
    const releaseValue = releaseFields.get(field);
    if (!checklistValue) {
      fail(`completed evidence checklist is missing release identity field ${field}`);
    }
    if (!releaseValue) {
      fail(`completed release notes are missing release identity field ${field}`);
    }
    if (normalizeEvidenceValue(checklistValue) !== normalizeEvidenceValue(releaseValue)) {
      fail(
        `release identity mismatch for ${field}: checklist has ${JSON.stringify(checklistValue)}, release notes have ${JSON.stringify(releaseValue)}`,
      );
    }
  }
}

function normalizeEvidencePath(path) {
  return path.replace(/\\/g, "/").replace(/^\.\//, "").replace(/\/+/g, "/").trim();
}

function candidateEvidencePaths(path) {
  const candidates = new Set();
  const resolved = resolve(path);
  const values = [
    path,
    normalizeEvidencePath(path),
    resolved,
    relative(process.cwd(), resolved),
    relative(repoRoot, resolved),
  ];
  for (const value of values) {
    if (value && value !== ".") {
      candidates.add(normalizeEvidencePath(value));
    }
  }
  if (isAbsolute(path)) {
    candidates.add(normalizeEvidencePath(path));
  }
  return candidates;
}

function validateChecklistPathReference(checklistPath, releaseNotesMarkdown) {
  const fields = releaseNotesFieldEntries(releaseNotesMarkdown);
  const recordedPath = fields.get("Completed evidence checklist path");
  if (!recordedPath || isPlaceholderValue(recordedPath)) {
    fail("release notes launch decision is missing Completed evidence checklist path");
  }

  const normalizedRecordedPath = normalizeEvidencePath(recordedPath);
  if (!candidateEvidencePaths(checklistPath).has(normalizedRecordedPath)) {
    fail(
      `release notes Completed evidence checklist path ${JSON.stringify(recordedPath)} does not match --checklist ${JSON.stringify(checklistPath)}`,
    );
  }
}

function validateChecklist(markdown, templateMarkdown = null) {
  validateNoRawSecrets("completed evidence checklist", markdown);

  for (const heading of requiredChecklistHeadings) {
    requireIncludes("completed evidence checklist", markdown, heading);
  }

  const items = checklistItems(markdown);
  if (items.length === 0) {
    fail("completed evidence checklist has no checkbox items");
  }

  const unchecked = items.filter(({ checked }) => !checked);
  if (unchecked.length > 0) {
    const first = unchecked[0];
    fail(
      `completed evidence checklist still has ${unchecked.length} unchecked item(s); first at line ${first.lineNumber}: ${first.line}`,
    );
  }

  const emptyField = items.find(({ text }) => {
    const normalized = normalizeChecklistText(text);
    return requiredChecklistFields.some((field) => {
      const value = checklistFieldValue(normalized, field);
      return value !== null && isPlaceholderValue(value);
    });
  });
  if (emptyField) {
    fail(`completed evidence checklist has an empty or placeholder field at line ${emptyField.lineNumber}: ${emptyField.line}`);
  }
  const checklistFields = checklistFieldEntries(markdown);
  validateMigrationClassification(
    "completed evidence checklist",
    checklistFields.get("Migration classification") || "",
  );

  if (templateMarkdown) {
    const templateItems = checklistItems(templateMarkdown).map(({ text }) =>
      normalizeChecklistText(text),
    );
    const completedTexts = new Set(items.map(({ text }) => normalizeChecklistText(text)));
    const missing = templateItems.filter(
      (templateText) => !checklistItemSatisfied(templateText, completedTexts),
    );
    if (missing.length > 0) {
      fail(`completed evidence checklist is missing required item: ${missing[0]}`);
    }
  }

  const launchApprovals = items.filter(
    ({ text }) =>
      text.includes("All required evidence above is complete.") ||
      text.includes("Operator approved public beta traffic."),
  );
  if (launchApprovals.length !== 2) {
    fail("launch decision must check both required evidence completion and operator approval");
  }
}

function validateReleaseNotes(markdown, templateMarkdown = null) {
  validateNoRawSecrets("completed release notes", markdown);

  for (const heading of requiredReleaseHeadings) {
    requireIncludes("completed release notes", markdown, heading);
  }

  const entries = releaseFieldEntries(markdown);
  if (entries.length === 0) {
    fail("completed release notes have no field entries");
  }

  const invalid = entries.filter((entry) => isPlaceholderValue(fieldValue(entry)));
  if (invalid.length > 0) {
    fail(`release notes contain empty or placeholder field values: ${invalid[0]}`);
  }

  const fields = new Map(entries.map((entry) => [fieldName(entry), fieldValue(entry)]));
  if (templateMarkdown) {
    const templateFields = releaseFieldEntries(templateMarkdown).map(fieldName);
    const missing = templateFields.filter((field) => !fields.has(field));
    if (missing.length > 0) {
      fail(`completed release notes are missing required field: ${missing[0]}`);
    }
  }

  for (const field of requiredLaunchDecisionFields) {
    const value = fields.get(field);
    if (!value || isPlaceholderValue(value)) {
      fail(`release notes launch decision is missing ${field}`);
    }
  }
  validateMigrationClassification(
    "completed release notes",
    fields.get("Migration classification") || "",
  );

  const validatorResult = fields.get("Release evidence validator result");
  if (!/^passed\b/i.test(validatorResult)) {
    fail("release notes must record Release evidence validator result as passed");
  }

  const operatorApproval = fields.get("Operator approval");
  if (!/^approved\b/i.test(operatorApproval)) {
    fail("release notes must record Operator approval as approved");
  }

  if (!/^yes$/i.test(fields.get("Public beta traffic accepted"))) {
    fail("release notes must set Public beta traffic accepted to yes");
  }
}

function completeChecklistFixture() {
  return readFileSync(checklistTemplatePath, "utf8")
    .replaceAll("- [ ] ", "- [x] ")
    .replace("- [x] Release commit:", "- [x] Release commit: abc1234")
    .replace(
      "- [x] Immutable image reference or digest:",
      "- [x] Immutable image reference or digest: ghcr.io/cvinit/pastebox:sha-abc1234",
    )
    .replace("- [x] Production domain:", "- [x] Production domain: pastebox.prod.testdomain")
    .replace("- [x] Deployment window:", "- [x] Deployment window: 2026-05-26T20:00Z")
    .replace("- [x] Operator:", "- [x] Operator: release-operator")
    .replace(
      "- [x] Previous known-good image:",
      "- [x] Previous known-good image: ghcr.io/cvinit/pastebox:sha-previous",
    )
    .replace(
      "- [x] Migration classification: no-migration / reversible /\n  forward-compatible / non-reversible",
      "- [x] Migration classification: reversible",
    );
}

function fixtureValueForField(field) {
  if (field === "Public beta traffic accepted") {
    return "yes";
  }
  if (field === "Migration classification") {
    return "reversible";
  }
  if (field === "Skipped checklist items with justification") {
    return "none, all required items completed";
  }
  if (field === "Release evidence validator result") {
    return "passed";
  }
  if (field === "Completed evidence checklist path") {
    return "evidence/rc-1/checklist.md";
  }
  if (field === "Operator approval") {
    return "approved by release-operator";
  }
  if (field === "Approval time") {
    return "2026-05-26T21:00Z";
  }
  if (field === "Immutable image reference or digest") {
    return "ghcr.io/cvinit/pastebox:sha-abc1234";
  }
  if (field === "Previous known-good image") {
    return "ghcr.io/cvinit/pastebox:sha-previous";
  }
  if (field === "Release commit") {
    return "abc1234";
  }
  if (field === "Production domain") {
    return "pastebox.prod.testdomain";
  }
  if (field === "Deployment window") {
    return "2026-05-26T20:00Z";
  }
  if (field === "Operator" || field === "Owner") {
    return "release-operator";
  }
  if (field === "Deadline" || field === "Launch impact") {
    return "none";
  }
  return `recorded ${field.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "")}`;
}

function completeReleaseNotesFixture(templateMarkdown = readFileSync(releaseNotesTemplatePath, "utf8")) {
  const fields = releaseFieldEntries(templateMarkdown).map(fieldName);
  const fieldLines = fields.map((field) => `- ${field}: ${fixtureValueForField(field)}`);
  return `${requiredReleaseHeadings.join("\n\n")}

${fieldLines.join("\n")}
`;
}

function removeReleaseField(markdown, field) {
  return markdown
    .split(/\r?\n/)
    .filter((line) => !line.startsWith(`- ${field}:`))
    .join("\n");
}

function assertSelfTestFailure(name, fn, expectedMessagePart) {
  try {
    fn();
  } catch (error) {
    if (error.message.includes(expectedMessagePart)) {
      return;
    }
    throw new Error(`${name} failed with unexpected message: ${error.message}`);
  }
  throw new Error(`${name} unexpectedly passed`);
}

function runSelfTest() {
  throwOnFailure = true;
  const checklistTemplate = readFileSync(checklistTemplatePath, "utf8");
  const releaseNotesTemplate = readFileSync(releaseNotesTemplatePath, "utf8");

  const releaseNotesFixture = completeReleaseNotesFixture(releaseNotesTemplate);
  const fixtureChecklistPath = "evidence/rc-1/checklist.md";

  validateChecklist(completeChecklistFixture(), checklistTemplate);
  validateReleaseNotes(releaseNotesFixture, releaseNotesTemplate);
  validateEvidenceConsistency(completeChecklistFixture(), releaseNotesFixture);
  validateChecklistPathReference(fixtureChecklistPath, releaseNotesFixture);

  assertSelfTestFailure(
    "unchecked checklist",
    () =>
      validateChecklist(
        completeChecklistFixture().replace(
          "- [x] Release commit: abc1234",
          "- [ ] Release commit: abc1234",
        ),
        checklistTemplate,
      ),
    "unchecked item",
  );
  assertSelfTestFailure(
    "missing checklist item",
    () =>
      validateChecklist(
        completeChecklistFixture().replace("- [x] `make test` passed for the release commit.\n", ""),
        checklistTemplate,
      ),
    "missing required item",
  );
  assertSelfTestFailure(
    "empty checklist field",
    () =>
      validateChecklist(
        completeChecklistFixture().replace("- [x] Release commit: abc1234", "- [x] Release commit:"),
        checklistTemplate,
      ),
    "empty or placeholder field",
  );
  assertSelfTestFailure(
    "invalid checklist migration classification",
    () =>
      validateChecklist(
        completeChecklistFixture().replace(
          "- [x] Migration classification: reversible",
          "- [x] Migration classification: risky",
        ),
        checklistTemplate,
      ),
    "migration classification must be one of",
  );
  assertSelfTestFailure(
    "missing release notes field",
    () =>
      validateReleaseNotes(
        removeReleaseField(releaseNotesFixture, "Stripe checkout result"),
        releaseNotesTemplate,
      ),
    "missing required field",
  );
  assertSelfTestFailure(
    "invalid release notes migration classification",
    () =>
      validateReleaseNotes(
        releaseNotesFixture.replace(
          "- Migration classification: reversible",
          "- Migration classification: risky",
        ),
        releaseNotesTemplate,
      ),
    "migration classification must be one of",
  );
  assertSelfTestFailure(
    "unapproved launch",
    () =>
      validateReleaseNotes(
        releaseNotesFixture.replace(
          "- Public beta traffic accepted: yes",
          "- Public beta traffic accepted: no",
        ),
        releaseNotesTemplate,
      ),
    "must set Public beta traffic accepted to yes",
  );
  assertSelfTestFailure(
    "failed release evidence validator result",
    () =>
      validateReleaseNotes(
        releaseNotesFixture.replace(
          "- Release evidence validator result: passed",
          "- Release evidence validator result: failed",
        ),
        releaseNotesTemplate,
      ),
    "must record Release evidence validator result as passed",
  );
  assertSelfTestFailure(
    "pending operator approval",
    () =>
      validateReleaseNotes(
        releaseNotesFixture.replace(
          "- Operator approval: approved by release-operator",
          "- Operator approval: pending release-operator review",
        ),
        releaseNotesTemplate,
      ),
    "must record Operator approval as approved",
  );
  assertSelfTestFailure(
    "placeholder release field",
    () =>
      validateReleaseNotes(
        releaseNotesFixture.replace(
          "- Immutable image reference or digest: ghcr.io/cvinit/pastebox:sha-abc1234",
          "- Immutable image reference or digest:",
        ),
        releaseNotesTemplate,
      ),
    "empty or placeholder",
  );
  assertSelfTestFailure(
    "raw secret in checklist",
    () =>
      validateChecklist(
        completeChecklistFixture().replace(
          "- [x] Operator: release-operator",
          "- [x] Operator: release-operator whsec_1234567890abcdef",
        ),
        checklistTemplate,
      ),
    "contains a raw Stripe webhook secret",
  );
  assertSelfTestFailure(
    "raw secret in release notes",
    () =>
      validateReleaseNotes(
        releaseNotesFixture.replace(
          "- Stripe checkout result: recorded stripe-checkout-result",
          "- Stripe checkout result: Bearer abcdefghijklmnopqrstuvwxyz123456",
        ),
        releaseNotesTemplate,
      ),
    "contains a raw bearer token",
  );
  assertSelfTestFailure(
    "mismatched release identity",
    () =>
      validateEvidenceConsistency(
        completeChecklistFixture(),
        releaseNotesFixture.replace("- Release commit: abc1234", "- Release commit: def5678"),
      ),
    "release identity mismatch",
  );
  assertSelfTestFailure(
    "mismatched checklist path",
    () =>
      validateChecklistPathReference(
        "evidence/rc-2/checklist.md",
        releaseNotesFixture,
      ),
    "does not match --checklist",
  );

  throwOnFailure = false;
  console.log("production release evidence self-test passed");
}

const options = parseArgs(process.argv.slice(2));
if (options.help) {
  usage();
  process.exit(0);
}
if (options.selfTest) {
  runSelfTest();
  process.exit(0);
}

const checklist = readRequired(options.checklist, "checklist");
const releaseNotes = readRequired(options["release-notes"], "release-notes");
const checklistTemplate = readRequired(checklistTemplatePath, "checklist template");
const releaseNotesTemplate = readRequired(releaseNotesTemplatePath, "release notes template");

validateChecklist(checklist, checklistTemplate);
validateReleaseNotes(releaseNotes, releaseNotesTemplate);
validateEvidenceConsistency(checklist, releaseNotes);
validateChecklistPathReference(options.checklist, releaseNotes);

console.log("production release evidence check passed");
