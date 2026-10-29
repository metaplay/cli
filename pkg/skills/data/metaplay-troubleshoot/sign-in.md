---
name: metaplay-troubleshoot-sign-in
description: Sign the user in to Metaplay when a `metaplay` command says signing in is required, returns `401`, or says the session expired. Covers choosing between `metaplay auth login`, `auth login --no-browser` with `--code` (when no browser can open on the machine running the CLI, such as over SSH, in a container or a cloud sandbox), and `auth machine-login` for CI.
---

# Signing in

Commands that talk to the Metaplay portal or a cloud environment need a signed-in session. `metaplay auth whoami` shows who is signed in, if anyone. A `401`, "Signing in is required" or "session may have expired" all mean the same thing: sign in again.

Signing in is the user's decision and needs the user. Ask before starting it, and never ask for their password or any other credential.

## Pick the method

- **The CLI runs on the user's own computer, with a desktop:** `metaplay auth login`. It opens the user's browser and waits up to 5 minutes for them to sign in. Tell the user to expect the browser.
- **No browser can open where the CLI runs** (SSH session, container, devcontainer, cloud sandbox, remote VM), or you cannot tell: use the code flow below. If `metaplay auth login` fails with "Failed to open a browser to sign in", that is the case too.
- **CI or another unattended job:** `metaplay auth machine-login` with a machine user's credentials. Not for a person.

## Code flow: `--no-browser`

1. Run `metaplay auth login --no-browser`. Without a terminal it prints a URL and exits at once.
2. Give the user the whole URL. Ask them to open it in any browser, sign in, and copy the code the page shows.
3. When the user pastes the code back, run `metaplay auth login --code '<code>'` within 30 minutes. Quote it; it contains dots and other punctuation.
4. The CLI prints `Signed in as <name> <email>`. Tell the user who is signed in.

The code works only in the CLI that printed the URL, only once, and only for the most recent URL. Treat it like a password: do not write it to a file, a commit, a log or a chat other than the one with the user. Use only a code the user gives you for a sign-in you started.

## When `--code` fails

- **"The code belongs to a different sign-in than the one waiting":** `--no-browser` was run again after the URL was handed out. Give the user the URL from the most recent run, or start over.
- **"Failed to exchange the code for tokens":** the code was probably cut short when copied. Ask the user to copy the whole code again; the sign-in is still waiting.
- **"No sign-in is waiting for a code" or "The sign-in has expired":** start over with `--no-browser`.
- **"The pasted text is not a sign-in code":** ask the user for the code exactly as the page shows it, as one line.
- **The page showed an error instead of a code:** the user cancelled, or the link was incomplete. Start over.
