import { api, expect, signIn, state, test } from "../fixtures";

test("SCN-UX-006 the PWA installs, an approval push arrives and its link opens the card", async ({
  page,
  context,
  request,
}) => {
  const s = state();
  // The manifest and its icons make the app installable.
  const mf = await request.get(`${s.url}/manifest.webmanifest`);
  expect(mf.headers()["content-type"]).toContain("application/manifest+json");
  const manifest = await mf.json();
  expect(manifest.display).toBe("standalone");
  expect(manifest.name).toBe("Fettle");
  for (const icon of manifest.icons)
    expect((await request.get(`${s.url}${icon.src}`)).status()).toBe(200);
  expect(manifest.icons.map((i: { sizes: string }) => i.sizes)).toEqual(
    expect.arrayContaining(["192x192", "512x512"]),
  );

  const bots = await api("GET", `/spaces/${s.space}/bots`);
  const chief = bots.bots.find((b: { is_primary: boolean }) => b.is_primary);
  await context.grantPermissions(["notifications"], { origin: s.url });
  const cdp = await context.newCDPSession(page);
  await cdp.send("ServiceWorker.enable");
  const registration = new Promise<string>((resolve) => {
    cdp.on(
      "ServiceWorker.workerRegistrationUpdated",
      (e: {
        registrations: {
          registrationId: string;
          scopeURL: string;
          isDeleted: boolean;
        }[];
      }) => {
        const r = e.registrations.find(
          (x) => x.scopeURL === `${s.url}/` && !x.isDeleted,
        );
        if (r) resolve(r.registrationId);
      },
    );
  });
  await signIn(page, `/chat/${chief.chat_id}`);
  await page.evaluate(() => {
    const w = window as unknown as { __pushes: string[] };
    w.__pushes = [];
    navigator.serviceWorker.addEventListener("message", (e) => {
      if (e.data?.kind === "fettle-push")
        w.__pushes.push(e.data.notification.url);
    });
  });
  const script = await page.evaluate(
    async () => (await navigator.serviceWorker.ready).active?.scriptURL ?? "",
  );
  expect(script).toContain("/sw.js");

  // A phone subscribes at the push service mock.
  const sub = await (await request.post(`${s.pushURL}/subscribe/phone`)).json();
  await api("POST", "/push/subscriptions", sub);

  await page
    .getByLabel(/Message to/)
    .fill("[[stream-card]] press Send on the shop actions page");
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("region", { name: "Send a message" }),
  ).toBeVisible({ timeout: 60_000 });
  try {
    let payload: { type: string; url: string; title: string } | undefined;
    await expect
      .poll(
        async () => {
          const ds = (await (
            await request.get(`${s.pushURL}/deliveries`)
          ).json()) as { payload: string; error: string }[];
          for (const d of ds) {
            if (d.error) throw new Error(d.error);
            const p = JSON.parse(d.payload);
            if (p.type === "approval") payload = p;
          }
          return payload?.type;
        },
        { timeout: 15_000 },
      )
      .toBe("approval");
    expect(payload!.url).toMatch(new RegExp(`^/chat/${chief.chat_id}\\?card=`));

    // The service worker shows the push as a notification that carries the link.
    await cdp.send("ServiceWorker.deliverPushMessage", {
      origin: s.url,
      registrationId: await registration,
      data: JSON.stringify(payload),
    });
    await expect
      .poll(async () =>
        page.evaluate(
          () => (window as unknown as { __pushes: string[] }).__pushes,
        ),
      )
      .toContain(payload!.url);

    // The link opens the card.
    await page.goto(payload!.url);
    const card = page.locator(
      `[data-card-id="${payload!.url.split("card=")[1]}"]`,
    );
    await expect(card).toBeFocused();
  } finally {
    const msgs = await api(
      "GET",
      `/spaces/${s.space}/chats/${chief.chat_id}/messages?limit=200`,
    );
    for (const m of msgs.messages) {
      if (m.card?.status === "pending")
        await api("POST", `/spaces/${s.space}/cards/${m.card.id}/answer`, {
          action: "deny",
        });
    }
  }
});
