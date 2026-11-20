# Shared sign-ins

You can give a bot your sign-in for one site from your own Chrome. The bot then does not sign in again.
The Fettle extension for Chrome reads the cookies and the local storage of that site.
It sends them to your Fettle server, and the server puts them into the browser of one bot.
Fettle keeps no copy.

A shared sign-in has the same result as a sign-in by takeover: the bot's browser keeps the session until the site ends it.

## Before you start

- The bot must work in a zone with the size `browser` or `desktop`. Other sizes have no browser.
- You must be signed in to the site in Chrome.
- Edge, Brave and Arc use the same extension. Firefox and Safari are not supported.

## Install the extension

1. Run `just extension`. The command builds the extension into `extension/dist`.
2. In Chrome, open `chrome://extensions`.
3. Turn on **Developer mode**.
4. Click **Load unpacked** and select the folder `extension/dist`.

## Connect Chrome

1. In Fettle, open **Settings**, **Access tokens**.
2. Click **Connect Chrome**. Fettle opens the pairing page in your default browser.
3. In Chrome, keep the pairing page open and click the Fettle icon in the toolbar.
4. Click **Connect**.

If your default browser is not Chrome, Fettle shows the server and a code. Open the extension in Chrome and type them.
The code works one time, for 10 minutes.

In the desktop app, the extension gets a token that can only share a sign-in. **Access tokens** lists it, and **Revoke** removes it.
In server mode, the extension gets an access token with the scope `share-sign-in`. **Access tokens** lists it.
A token that can only share a sign-in gets 403 on every other route.

## Share a sign-in

1. In Chrome, open the site, for example `https://github.com`.
2. Click the Fettle icon.
3. Select the bot. The extension shows how many people can ask the bot to act with your sign-in.
4. Click **Share sign-in**. Chrome asks for access to this site only.

The extension shares the cookies of the site's domain and of its subdomains, and the local storage of the open page.
A site that signs you in through another domain, for example a Google sign-in, needs a second share for that domain.

## When a bot asks

When a bot must be signed in, it posts a takeover card with the site.
The card has **Share from Chrome**. The button opens the site in your browser.
The Fettle icon shows how many sign-ins your bots wait for.
After the share, the run continues, as after **Done**.
The bot opens the site and checks the sign-in. When it is still signed out, it asks for a takeover.

Some sites bind a session to one device, for example a Google account in Chrome on Windows. A shared session for such a site does not work. Use the takeover.

## Sign a bot out

1. Open the bot page.
2. In **Signed-in sites**, click **Show the sites**. Fettle starts the computer to read them.
3. Click **Sign out** next to a site.

**Sign out** removes the cookies and the stored data of the site, also for a sign-in by takeover.

## What Fettle records

- Each share and each sign-out writes an audit entry with the site, the bot, the person and the time. The entry holds no value.
- Fettle holds the values in memory only, until the bot's browser has them. The secret replacement knows them for that time, so no value reaches a log or an event.
- The [privacy policy of the extension](chrome-extension-privacy.md) describes what the extension reads and sends.
