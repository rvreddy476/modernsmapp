// osv-gate.mjs <osv-scanner.json>
//
// Reads `osv-scanner --format json` output, prints every finding (package,
// version, id, severity, fixed version) and exits 1 when a HIGH or CRITICAL
// finding has a fixed version. Everything else only prints. Gate line 5
// (dependency scans); used by .github/workflows/android-native.yml.
//
// Severity: the GitHub advisory rating (database_specific.severity) when the
// record has one, else osv-scanner's group max_severity CVSS score
// (>= 9 critical, >= 7 high, >= 4 medium). A finding with neither counts as
// HIGH, so an unrated record fails closed when it has a fix.
// Plain Node (a GitHub runner has it); no dependencies.
import { readFileSync } from "node:fs";

const file = process.argv[2];
if (!file) {
  console.error("usage: node osv-gate.mjs <osv-scanner.json>");
  process.exit(2);
}
const text = readFileSync(file, "utf8");
if (!text.trim()) {
  console.log("::error title=osv-scanner printed nothing::no JSON report");
  process.exit(2);
}
const report = JSON.parse(text);
if (!Array.isArray(report.results)) {
  console.log("::error title=osv-scanner::unexpected report shape (no results array)");
  process.exit(2);
}

const rank = { CRITICAL: 4, HIGH: 3, MEDIUM: 2, MODERATE: 2, LOW: 1 };
const fromScore = (s) => {
  const n = Number.parseFloat(s);
  if (Number.isNaN(n)) return null;
  return n >= 9 ? "CRITICAL" : n >= 7 ? "HIGH" : n >= 4 ? "MEDIUM" : "LOW";
};

const rows = [];
for (const result of report.results) {
  for (const p of result.packages || []) {
    const name = p.package?.name;
    const version = p.package?.version;
    const groupScore = new Map();
    for (const g of p.groups || []) for (const id of g.ids || []) groupScore.set(id, g.max_severity);
    for (const v of p.vulnerabilities || []) {
      const rated = String(v.database_specific?.severity || "").toUpperCase();
      const sev = rank[rated] ? (rated === "MODERATE" ? "MEDIUM" : rated) : fromScore(groupScore.get(v.id)) || "HIGH";
      const fixes = new Set();
      for (const a of v.affected || []) {
        if (a.package?.name !== name) continue;
        for (const r of a.ranges || []) for (const e of r.events || []) if (e.fixed) fixes.add(e.fixed);
      }
      rows.push({ name, version, id: v.id, sev, fix: [...fixes].join(" | "), summary: v.summary || "" });
    }
  }
}

rows.sort((x, y) => rank[y.sev] - rank[x.sev] || x.name.localeCompare(y.name) || x.id.localeCompare(y.id));
const blocking = rows.filter((r) => rank[r.sev] >= rank.HIGH && r.fix);
for (const r of rows) {
  const tag = blocking.includes(r) ? "BLOCK" : "warn ";
  console.log(
    `${tag} ${r.sev.padEnd(8)} ${r.name} ${r.version}  ${r.id}  fixed ${r.fix || "none"}  ${r.summary.slice(0, 90)}`,
  );
}
const count = (s) => rows.filter((r) => r.sev === s).length;
console.log(
  `${rows.length} findings: ${count("CRITICAL")} critical, ${count("HIGH")} high, ` +
    `${count("MEDIUM")} medium, ${count("LOW")} low; ${blocking.length} high/critical with a fix`,
);
if (blocking.length) {
  for (const r of blocking) {
    console.log(`::error title=Vulnerable dependency::${r.name} ${r.version} ${r.sev} ${r.id}, fixed in ${r.fix}`);
  }
  process.exit(1);
}
