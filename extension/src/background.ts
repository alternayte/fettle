import { loadConnection, targets } from "./api";

// The badge shows how many open sign-in requests the bots of this account have, so that a
// person sees that a bot waits for a sign-in.

async function refresh() {
  const c = await loadConnection();
  if (!c) {
    await chrome.action.setBadgeText({ text: "" });
    return;
  }
  try {
    const t = await targets(c);
    const n = t.spaces.reduce((sum, s) => sum + s.bots.reduce((m, b) => m + b.requests.length, 0), 0);
    await chrome.action.setBadgeBackgroundColor({ color: "#4f7a6a" });
    await chrome.action.setBadgeText({ text: n > 0 ? String(n) : "" });
  } catch {
    await chrome.action.setBadgeText({ text: "" });
  }
}

chrome.runtime.onInstalled.addListener(() => {
  void chrome.alarms.create("refresh", { periodInMinutes: 1 });
  void refresh();
});
chrome.runtime.onStartup.addListener(() => void refresh());
chrome.alarms.onAlarm.addListener((a) => {
  if (a.name === "refresh") void refresh();
});
chrome.storage.onChanged.addListener((changes) => {
  if (changes.connection) void refresh();
});
