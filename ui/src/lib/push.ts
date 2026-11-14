import { api, must } from "@/api/client";

/** pushSupported reports whether this browser can receive Web Push. */
export function pushSupported(): boolean {
  return "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
}

function keyBytes(base64url: string): Uint8Array<ArrayBuffer> {
  const pad = "=".repeat((4 - (base64url.length % 4)) % 4);
  const raw = atob((base64url + pad).replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

/** enablePush asks for permission, subscribes this device and stores the subscription. */
export async function enablePush(): Promise<"on" | "denied"> {
  if ((await Notification.requestPermission()) !== "granted") return "denied";
  const reg = await navigator.serviceWorker.ready;
  const { public_key } = await must(api.GET("/api/v1/push/key"));
  const sub =
    (await reg.pushManager.getSubscription()) ??
    (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(public_key) }));
  const j = sub.toJSON();
  await must(
    api.POST("/api/v1/push/subscriptions", {
      body: { endpoint: j.endpoint ?? "", keys: { p256dh: j.keys?.p256dh ?? "", auth: j.keys?.auth ?? "" } },
    }),
  );
  return "on";
}

/** disablePush removes this device's subscription. */
export async function disablePush(): Promise<void> {
  const reg = await navigator.serviceWorker.ready;
  const sub = await reg.pushManager.getSubscription();
  if (!sub) return;
  await must(api.POST("/api/v1/push/subscriptions/remove", { body: { endpoint: sub.endpoint } }));
  await sub.unsubscribe();
}

/** devicePushOn reports whether this device has a subscription. */
export async function devicePushOn(): Promise<boolean> {
  if (!pushSupported()) return false;
  const reg = await navigator.serviceWorker.getRegistration();
  return !!(await reg?.pushManager.getSubscription());
}
