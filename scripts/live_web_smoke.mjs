import { existsSync } from "node:fs";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";

const API_BASE = "http://localhost:8080";
const WEB_BASE = "https://localhost";
const PASSWORD = "Test@1234";
const HOST_PORT = 9333;
const VIEWER_PORT = 9334;
const WORKSPACE_TMP = path.join(process.cwd(), ".tmp");

const browserCandidates = [
  "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
  "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
];

function getBrowserPath() {
  const browserPath = browserCandidates.find((candidate) => existsSync(candidate));
  if (!browserPath) {
    throw new Error("No supported browser binary found for headless smoke test");
  }
  return browserPath;
}

async function fetchJson(url, options = {}) {
  const response = await fetch(url, options);
  const text = await response.text();
  let payload = null;
  try {
    payload = text ? JSON.parse(text) : null;
  } catch {
    payload = text;
  }
  if (!response.ok) {
    throw new Error(`${options.method || "GET"} ${url} failed (${response.status}): ${JSON.stringify(payload)}`);
  }
  return payload;
}

async function registerUser(label) {
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const email = `${label}-${stamp}@example.com`;
  const payload = {
    email,
    password: PASSWORD,
    first_name: label === "host" ? "Live" : "Viewer",
    last_name: label === "host" ? "Host" : "Guest",
    dob: "1999-01-01",
    gender: "other",
  };
  const register = await fetchJson(`${API_BASE}/v1/auth/register`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(payload),
  });
  const user = register?.data?.user;
  const tokens = register?.data?.tokens;
  if (!user?.id || !tokens?.access_token || !tokens?.refresh_token) {
    throw new Error(`Registration response for ${label} is missing user/tokens`);
  }
  const profile = await fetchJson(`${API_BASE}/v1/profiles/me`, {
    headers: {
      Authorization: `Bearer ${tokens.access_token}`,
      "X-User-Id": user.id,
    },
  });
  return {
    email,
    user,
    profile,
    tokenRecord: {
      accessToken: tokens.access_token,
      refreshToken: tokens.refresh_token,
      updatedAt: Date.now(),
    },
  };
}

async function waitForUrl(url, timeoutMs = 15000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) {
        return await response.json();
      }
    } catch {
      // wait for the devtools endpoint to come up
    }
    await delay(250);
  }
  throw new Error(`Timed out waiting for ${url}`);
}

class CdpClient {
  constructor(socket) {
    this.socket = socket;
    this.nextId = 0;
    this.pending = new Map();
    this.listeners = new Map();

    socket.addEventListener("message", (event) => {
      const message = JSON.parse(event.data.toString());
      if (typeof message.id === "number") {
        const pending = this.pending.get(message.id);
        if (!pending) return;
        this.pending.delete(message.id);
        if (message.error) {
          pending.reject(new Error(message.error.message || JSON.stringify(message.error)));
          return;
        }
        pending.resolve(message.result);
        return;
      }
      const handlers = this.listeners.get(message.method);
      if (!handlers) return;
      for (const handler of handlers) {
        handler(message.params ?? {});
      }
    });
  }

  send(method, params = {}) {
    const id = ++this.nextId;
    const payload = { id, method, params };
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify(payload));
    });
  }

  on(method, handler) {
    const handlers = this.listeners.get(method) ?? new Set();
    handlers.add(handler);
    this.listeners.set(method, handlers);
    return () => handlers.delete(handler);
  }

  async waitForEvent(method, predicate = () => true, timeoutMs = 15000) {
    return await new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        off();
        reject(new Error(`Timed out waiting for ${method}`));
      }, timeoutMs);
      const off = this.on(method, (params) => {
        if (!predicate(params)) return;
        clearTimeout(timer);
        off();
        resolve(params);
      });
    });
  }

  async close() {
    if (this.socket.readyState === WebSocket.OPEN || this.socket.readyState === WebSocket.CONNECTING) {
      this.socket.close();
    }
  }
}

async function connectPageClient(port) {
  const targets = await waitForUrl(`http://127.0.0.1:${port}/json/list`);
  const target = targets.find((item) => item.type === "page" && item.webSocketDebuggerUrl);
  if (!target?.webSocketDebuggerUrl) {
    throw new Error(`No page target available on port ${port}`);
  }
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`Timed out opening CDP websocket on ${port}`)), 10000);
    socket.addEventListener("open", () => {
      clearTimeout(timer);
      resolve();
    });
    socket.addEventListener("error", () => {
      clearTimeout(timer);
      reject(new Error(`Failed to open CDP websocket on ${port}`));
    });
  });
  const client = new CdpClient(socket);
  await client.send("Page.enable");
  await client.send("Runtime.enable");
  await client.send("Network.enable");
  return client;
}

function launchBrowser(role, port) {
  const browserPath = getBrowserPath();
  const userDataDir = path.join(WORKSPACE_TMP, `live-smoke-${role}-${Date.now()}`);
  const child = spawn(
    browserPath,
    [
      "--headless=new",
      `--remote-debugging-port=${port}`,
      `--user-data-dir=${userDataDir}`,
      "--no-first-run",
      "--no-default-browser-check",
      "--ignore-certificate-errors",
      "--allow-insecure-localhost",
      "--disable-gpu",
      "--window-size=1440,1200",
      "about:blank",
    ],
    {
      stdio: "ignore",
    },
  );
  return { child, port, userDataDir };
}

async function navigate(client, url) {
  const loadEvent = client.waitForEvent("Page.loadEventFired", () => true, 30000);
  await client.send("Page.navigate", { url });
  await loadEvent;
}

async function evaluate(client, expression) {
  const result = await client.send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (result.exceptionDetails) {
    throw new Error(result.exceptionDetails.text || "Runtime evaluation failed");
  }
  return result.result?.value;
}

async function evaluateFn(client, fn, arg) {
  const expression = `(${fn.toString()})(${JSON.stringify(arg)})`;
  return await evaluate(client, expression);
}

async function waitForCondition(client, fn, arg, label, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const matched = await evaluateFn(client, fn, arg);
      if (matched) return matched;
    } catch {
      // page may still be navigating
    }
    await delay(250);
  }
  throw new Error(`Timed out waiting for ${label}`);
}

async function seedSession(client, session) {
  await navigate(client, `${WEB_BASE}/login`);
  await evaluateFn(
    client,
    ({ sessionUser, tokenRecord }) => {
      localStorage.setItem("postbook_session", JSON.stringify(sessionUser));
      localStorage.setItem("postbook_auth_tokens", JSON.stringify(tokenRecord));
      window.dispatchEvent(new Event("postbook:session-changed"));
      return true;
    },
    {
      sessionUser: session.user,
      tokenRecord: session.tokenRecord,
    },
  );
}

async function setFieldByPlaceholder(client, placeholder, value, tagName = "input") {
  const ok = await evaluateFn(
    client,
    ({ placeholder, value, tagName }) => {
      const root = tagName === "textarea" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      const setter = Object.getOwnPropertyDescriptor(root, "value")?.set;
      const elements = Array.from(document.querySelectorAll(tagName));
      const target = elements.find((element) => element.placeholder === placeholder);
      if (!target || !setter) return false;
      target.focus();
      setter.call(target, value);
      target.dispatchEvent(new Event("input", { bubbles: true }));
      target.dispatchEvent(new Event("change", { bubbles: true }));
      return true;
    },
    { placeholder, value, tagName },
  );
  if (!ok) {
    throw new Error(`Could not find ${tagName} with placeholder "${placeholder}"`);
  }
}

async function clickButtonByText(client, text) {
  const ok = await evaluateFn(
    client,
    (text) => {
      const button = Array.from(document.querySelectorAll("button")).find(
        (element) => element.textContent?.trim().includes(text) && !element.disabled,
      );
      if (!button) return false;
      button.click();
      return true;
    },
    text,
  );
  if (!ok) {
    throw new Error(`Could not click button "${text}"`);
  }
}

async function clickPinForMessage(client, messageText) {
  const ok = await evaluateFn(
    client,
    (messageText) => {
      const cards = Array.from(document.querySelectorAll("div.rounded-xl.bg-brand-card"));
      const card = cards.find((element) => element.textContent?.includes(messageText));
      const pinButton = card
        ? Array.from(card.querySelectorAll("button")).find(
            (button) => button.textContent?.trim() === "Pin" && !button.disabled,
          )
        : null;
      if (!pinButton) return false;
      pinButton.click();
      return true;
    },
    messageText,
  );
  if (!ok) {
    throw new Error(`Could not pin message "${messageText}"`);
  }
}

async function clickMuteForMessage(client, messageText) {
  const ok = await evaluateFn(
    client,
    (messageText) => {
      const cards = Array.from(document.querySelectorAll("div.rounded-xl.bg-brand-card"));
      const card = cards.find((element) => element.textContent?.includes(messageText));
      const muteButton = card
        ? Array.from(card.querySelectorAll("button")).find(
            (button) => button.textContent?.trim() === "Mute" && !button.disabled,
          )
        : null;
      if (!muteButton) return false;
      muteButton.click();
      return true;
    },
    messageText,
  );
  if (!ok) {
    throw new Error(`Could not mute viewer message "${messageText}"`);
  }
}

async function getTextContent(client) {
  return await evaluate(client, "document.body.innerText");
}

async function getViewerLink(client) {
  const href = await evaluateFn(
    client,
    () => {
      const link = Array.from(document.querySelectorAll("a")).find((element) =>
        element.textContent?.trim().includes("Open Viewer Page"),
      );
      return link ? link.href : null;
    },
    null,
  );
  if (!href) {
    throw new Error("Could not find viewer page link after going live");
  }
  return href;
}

async function saveSnapshot(client, filename) {
  const body = await getTextContent(client);
  await writeFile(path.join(WORKSPACE_TMP, filename), body, "utf8");
}

async function main() {
  await mkdir(WORKSPACE_TMP, { recursive: true });

  const hostSession = await registerUser("live-host");
  const viewerSession = await registerUser("live-viewer");

  const hostBrowser = launchBrowser("host", HOST_PORT);
  const viewerBrowser = launchBrowser("viewer", VIEWER_PORT);

  let hostClient;
  let viewerClient;

  try {
    await waitForUrl(`http://127.0.0.1:${HOST_PORT}/json/version`);
    await waitForUrl(`http://127.0.0.1:${VIEWER_PORT}/json/version`);

    hostClient = await connectPageClient(HOST_PORT);
    viewerClient = await connectPageClient(VIEWER_PORT);

    await seedSession(hostClient, hostSession);
    await seedSession(viewerClient, viewerSession);

    await navigate(hostClient, `${WEB_BASE}/live/start`);
    await waitForCondition(
      hostClient,
      (text) => document.body.innerText.includes(text),
      "Live Control Room",
      "host control room",
      30000,
    );

    const streamTitle = `Browser Live Smoke ${Date.now()}`;
    const viewerMessage = `Viewer smoke message ${Date.now()}`;

    await setFieldByPlaceholder(hostClient, "What are you streaming?", streamTitle, "input");
    await setFieldByPlaceholder(hostClient, "Tell followers what this stream is about.", "Headless browser smoke flow", "textarea");
    await clickButtonByText(hostClient, "Prepare Stream Key");
    await waitForCondition(
      hostClient,
      (text) => document.body.innerText.includes(text),
      "Current Stream",
      "prepared stream card",
      30000,
    );

    await clickButtonByText(hostClient, "Go Live");
    await waitForCondition(
      hostClient,
      (text) => document.body.innerText.includes(text),
      "End Stream",
      "live control room state",
      30000,
    );

    const viewerUrl = await getViewerLink(hostClient);
    await navigate(viewerClient, viewerUrl);
    await waitForCondition(
      viewerClient,
      (text) => document.body.innerText.includes(text),
      streamTitle,
      "viewer stream title",
      30000,
    );
    await waitForCondition(
      hostClient,
      () => {
        const text = document.body.innerText;
        return text.includes("Live Viewers") && /\b1\b/.test(text);
      },
      null,
      "viewer join count",
      30000,
    );

    await setFieldByPlaceholder(viewerClient, "Send a comment", viewerMessage, "textarea");
    await clickButtonByText(viewerClient, "Send");
    await waitForCondition(
      viewerClient,
      (text) => document.body.innerText.includes(text),
      viewerMessage,
      "viewer comment echo",
      30000,
    );
    await waitForCondition(
      hostClient,
      (text) => document.body.innerText.includes(text),
      viewerMessage,
      "host realtime chat fanout",
      30000,
    );

    await clickPinForMessage(hostClient, viewerMessage);
    await waitForCondition(
      viewerClient,
      (text) => {
        const bodyText = document.body.innerText.toUpperCase();
        return bodyText.includes("PINNED COMMENT") && document.body.innerText.includes(text);
      },
      viewerMessage,
      "viewer pinned banner",
      30000,
    );

    await setFieldByPlaceholder(hostClient, "Add blocked word or phrase", "forbidden", "input");
    await clickButtonByText(hostClient, "Add");
    await waitForCondition(
      hostClient,
      (text) => document.body.innerText.includes(text),
      "forbidden",
      "blocked word chip",
      30000,
    );

    await clickMuteForMessage(hostClient, viewerMessage);
    await waitForCondition(
      viewerClient,
      (text) => document.body.innerText.includes(text),
      "The host has muted you in this stream.",
      "viewer muted state",
      30000,
    );

    await saveSnapshot(hostClient, "live-smoke-host.txt");
    await saveSnapshot(viewerClient, "live-smoke-viewer.txt");

    console.log(`HOST_USER=${hostSession.user.id}`);
    console.log(`VIEWER_USER=${viewerSession.user.id}`);
    console.log(`STREAM_TITLE=${streamTitle}`);
    console.log(`VIEWER_URL=${viewerUrl}`);
    console.log("RESULT=PASS");
  } finally {
    try {
      await hostClient?.close();
    } catch {
      // ignore cleanup errors
    }
    try {
      await viewerClient?.close();
    } catch {
      // ignore cleanup errors
    }
    hostBrowser.child.kill();
    viewerBrowser.child.kill();
  }
}

main().catch((error) => {
  console.error(`RESULT=FAIL ${error.message}`);
  process.exit(1);
});

