// Run with Node's test runner and Playwright on the module path; see README.md.
// When each window of an account starts again is in sight at any use, not
// only near its cap, on the Usage page's accounts in brief and on the
// accounts in a provider's editor. Several Claude seats are picked by
// which renews first, and with only the tooltip a seat at 4% showed no
// reset time at all while one at 100% did (a phone has no hover). The
// account in full keeps its reset line as it was; a window with no reset
// time shows none. English and Chinese, at 900px and 440px; no backend,
// the API is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const inHours = (h) => new Date(Date.now() + h * 3600e3 + 60e3).toISOString();
const ago = (m) => new Date(Date.now() - m * 60e3).toISOString();
const seat = (user, five, week, served) => ({
  provider: "claude", name: "Claude Code", kind: "subscription", icon: "claude-color", plan: "team", user,
  windows: [
    { name: "5 hours", used: five, ...(five === null ? { used: 0 } : { resetsAt: inHours(3) }) },
    { name: "7 days", used: week, resetsAt: inHours(100) },
  ],
  ...(served ? { lastServedAt: served } : {}),
});
// a at 4% answered last (in full); b at 30%, c with no 5-hour reset known
const quotas = () => [seat("a@example.com", 4, 1, ago(1)), seat("b@example.com", 30, 2), seat("c@example.com", null, 3)];
const logins = () => ({
  "a@example.com": { provider: "claude", windows: quotas()[0].windows },
  "b@example.com": { provider: "claude", windows: quotas()[1].windows },
  "c@example.com": { provider: "claude", windows: quotas()[2].windows },
});
const providers = () => ({
  providers: [{
    id: "claude", name: "Claude Code", icon: "claudecode-color", chat: "", responses: "", anthropic: "https://api.anthropic.com", catalog: "anthropic",
    models: [{ id: "claude-opus-5-5", name: "", on: true }], agents: [], fallback: [], headers: {}, keyList: [], proxy: "",
    account: {
      agent: "claude", agentName: "Claude Code", user: "a@example.com", plan: "team",
      logins: ["a@example.com", "b@example.com", "c@example.com"].map((user, i) => ({ user, plan: "team", on: true, ...(i ? {} : { active: true }) })),
    },
  }],
  presets: [], excluded: [], gateway: { running: true, window: true },
});

function serve(lang) {
  const settings = { theme: "light", lang, tray: "panel", quotaLeft: false, currency: "usd" };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings });
    if (url.pathname === "/api/settings") return json(settings);
    if (url.pathname === "/api/providers") return json(providers());
    if (url.pathname === "/api/usage/quotas") return json(quotas());
    if (url.pathname === "/api/login/usage") return json(url.searchParams.get("agent") === "claude" ? logins() : {});
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { await route.fulfill({ status: 404, body: "" }); }
  };
}

const words = {
  en: { brief: /^Resets .+ · in 3h$/, week: /^Resets .+ · in 4d$/, inThree: "resets in 3h", inFour: "resets in 4d" },
  zh: { brief: /^重置于 .+ · 3 小时后$/, week: /^重置于 .+ · 4 天后$/, inThree: "3 小时后重置", inFour: "4 天后重置" },
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    const w = words[lang];
    const launch = async (t, width, name) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      const page = await (await browser.newContext({ viewport: { width, height: 760 }, reducedMotion: "reduce" })).newPage();
      t.after(async () => {
        if (process.env.ARTIFACT_DIR) {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-${width}-${name}-seat-reset.png`), fullPage: true });
        }
        await browser.close();
      });
      page.setDefaultTimeout(5000);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", serve(lang));
      return { page, errors };
    };

    for (const width of [900, 440]) {
      test(`${engine} ${lang} ${width}px: the Usage page's accounts in brief say when each window resets`, async (t) => {
        const { page, errors } = await launch(t, width, "usage");
        await page.goto("http://magpie.test/?view=usage");
        await page.waitForSelector(".subscription-account.brief .user", { state: "attached" });
        const rows = await page.evaluate(() => [...document.querySelectorAll(".subscription-account")].map((a) => {
          const meters = a.nextElementSibling;
          const seen = (sel) => [...meters.querySelectorAll(sel)].filter((e) => e.offsetParent).map((e) => e.textContent.trim());
          const card = a.closest(".subscription-card").getBoundingClientRect();
          return {
            user: a.querySelector(".user").title, brief: a.classList.contains("brief"),
            lines: seen(".quota-reset-brief"), full: seen(".quota-reset").length,
            // nothing spills out of the card
            inside: [...meters.querySelectorAll(".quota-reset-brief")].every((e) => e.getBoundingClientRect().right <= card.right + 0.5),
          };
        }));
        const by = (u) => rows.find((r) => r.user === u);
        // in full: its reset lines as before, and no second, brief one
        assert.equal(by("a@example.com").brief, false);
        assert.deepEqual(by("a@example.com").lines, []);
        assert.equal(by("a@example.com").full, 2);
        // in brief at 30%: the five hours and the week, each with its reset
        assert.equal(by("b@example.com").brief, true);
        assert.equal(by("b@example.com").full, 0);
        assert.equal(by("b@example.com").lines.length, 2, JSON.stringify(by("b@example.com")));
        assert.match(by("b@example.com").lines[0], w.brief);
        assert.match(by("b@example.com").lines[1], w.week);
        // no 5-hour reset known: only the week says one
        assert.equal(by("c@example.com").lines.length, 1);
        assert.match(by("c@example.com").lines[0], w.week);
        assert.ok(rows.every((r) => r.inside), "a reset line spills out of its card");
        assert.deepEqual(errors, []);
      });
    }

    test(`${engine} ${lang}: a provider's accounts say when each window resets, at 4% as at 100%`, async (t) => {
      const { page, errors } = await launch(t, 900, "editor");
      await page.goto("http://magpie.test/?view=providers");
      await page.locator(".row.provider", { hasText: "Claude Code" }).first().click();
      await page.locator(".editor .accts .acc .aq-w").first().waitFor();
      const resets = (id) => page.locator(`.editor .accts .acc[data-account-id="${id}"] .aq-w`).evaluateAll((ms) => ms.map((m) => m.querySelector(".aq-r")?.textContent ?? null));
      assert.deepEqual(await resets("a@example.com"), [w.inThree, w.inFour]);
      assert.deepEqual(await resets("b@example.com"), [w.inThree, w.inFour]);
      // a window with no reset time says none
      assert.deepEqual(await resets("c@example.com"), [null, w.inFour]);
      assert.deepEqual(errors, []);
    });
  }
}
