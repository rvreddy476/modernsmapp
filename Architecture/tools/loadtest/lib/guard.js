// Safety guard shared by every load-test script in this folder.
//
// Call assertSafeTarget() at the top of each script, in the init context,
// before anything else runs. A throw there aborts k6 before a single
// request is sent.
//
// The guard refuses to run unless ALL of these hold:
//   1. TARGET_ENV is one of ALLOWED_TARGET_ENVS (qa, staging, loadtest).
//   2. BASE_URL is an http(s) URL whose host names that environment as one
//      of its DNS labels or hyphen-separated tokens (for example
//      "api.staging.example.net" or "gw-loadtest.internal"). A host that
//      does not say which environment it is cannot be told apart from
//      production, so it is refused.
//   3. BASE_URL's host does not look like a production host: it must not
//      contain a "prod", "production", "live" or "www" token, must not be
//      a bare apex domain ("example.com") or "api." on a bare apex
//      ("api.example.com"), and must not match PROD_HOST_PATTERN when that
//      env var is set (CI passes it from a repository variable, so no real
//      hostname is written in this repository).
//   4. For scripts that write (likes, comments, follows, progress,
//      uploads), ALLOW_WRITES=yes is also set.
//
// No hostnames of real deployments appear in this file by design.

export const ALLOWED_TARGET_ENVS = ['qa', 'staging', 'loadtest'];

const BUILTIN_PROD_SHAPES = [
  /(^|[.-])(prod|production|live|www)([.-]|$)/,
  /^[^.]+\.[a-z]{2,}$/, // bare apex: example.com
  /^api\.[^.]+\.[a-z]{2,}$/, // api on a bare apex: api.example.com
];

function fail(msg) {
  throw new Error(`[loadtest guard] REFUSED: ${msg}`);
}

export function parseBaseURL(raw) {
  const m = /^(https?):\/\/([^/:?#]+)(:\d+)?(\/[^?#]*)?$/i.exec(raw || '');
  if (!m) {
    fail(`BASE_URL must be an absolute http(s) URL with no query, got "${raw || ''}"`);
  }
  return { scheme: m[1].toLowerCase(), host: m[2].toLowerCase(), port: m[3] || '', path: (m[4] || '').replace(/\/+$/, '') };
}

export function assertSafeTarget(opts) {
  const writes = !!(opts && opts.writes);
  const env = String(__ENV.TARGET_ENV || '').trim().toLowerCase();
  if (!ALLOWED_TARGET_ENVS.includes(env)) {
    fail(`TARGET_ENV must be one of ${ALLOWED_TARGET_ENVS.join(', ')}; got "${env}"`);
  }

  const base = String(__ENV.BASE_URL || '').trim();
  const { host } = parseBaseURL(base);

  const tokens = host.split(/[.-]/);
  if (!tokens.includes(env)) {
    fail(`BASE_URL host "${host}" does not name TARGET_ENV "${env}" as a label or token`);
  }

  for (const re of BUILTIN_PROD_SHAPES) {
    if (re.test(host)) {
      fail(`BASE_URL host "${host}" has a production shape (${re})`);
    }
  }

  const extra = String(__ENV.PROD_HOST_PATTERN || '').trim();
  if (extra) {
    let re;
    try {
      re = new RegExp(extra, 'i');
    } catch (e) {
      fail(`PROD_HOST_PATTERN is not a valid regular expression: ${e}`);
    }
    if (re.test(host)) {
      fail(`BASE_URL host "${host}" matches PROD_HOST_PATTERN`);
    }
  }

  if (writes && String(__ENV.ALLOW_WRITES || '') !== 'yes') {
    fail('this scenario writes data; set ALLOW_WRITES=yes to confirm the target is a disposable data set');
  }

  return base.replace(/\/+$/, '');
}
