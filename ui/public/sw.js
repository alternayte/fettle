// Fettle service worker: it shows Web Push notifications and opens their card or run.
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

self.addEventListener("push", (event) => {
  let n = { title: "Fettle", body: "", url: "/", type: "", tag: "" };
  try {
    n = { ...n, ...event.data.json() };
  } catch {
    if (event.data) n.body = event.data.text();
  }
  const shown = self.registration.showNotification(n.title, {
    body: n.body,
    icon: "/icon-192.png",
    badge: "/icon-192.png",
    tag: n.tag || undefined,
    data: { url: n.url, type: n.type },
    requireInteraction: n.type === "approval" || n.type === "question" || n.type === "takeover",
  });
  // Open windows hear about the push too, so they can refresh at once.
  const told = self.clients
    .matchAll({ type: "window", includeUncontrolled: true })
    .then((wins) => wins.forEach((w) => w.postMessage({ kind: "fettle-push", notification: n })));
  event.waitUntil(Promise.all([shown, told]));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = new URL(event.notification.data?.url || "/", self.location.origin).href;
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((wins) => {
      for (const w of wins) {
        if (new URL(w.url).origin === self.location.origin && "navigate" in w) {
          return w.focus().then(() => w.navigate(url));
        }
      }
      return self.clients.openWindow(url);
    }),
  );
});
