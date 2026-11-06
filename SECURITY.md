# Security Policy

## Supported versions

Holt is developed on `main`; the latest release is the supported version. Older builds are not patched.

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Use GitHub's private vulnerability reporting:
https://github.com/holthq/holt/security/advisories/new

Include:

- what the problem is and what it lets an attacker do,
- how to reproduce it (version from Holt → About Holt, OS, steps),
- any log or crash output from About → Open Logs Folder.

We aim to acknowledge reports within 7 days and to publish an advisory once a fix is available. Please give us a
reasonable amount of time before disclosing the issue publicly.

## Scope

- Code in this repository (app, terminal daemon, Git integration, file operations).
- Third-party components (`mygo`, `libghostty-vt`, fonts, icon sets) are maintained upstream; please report those
  upstream as well, and mention it here if it affects Holt.

## What Holt does with the network

Holt only connects to the network when you ask it to: Git fetch/pull/push/clone, opening links in the browser, and
Check for Updates (a read-only request to the GitHub releases API). It does not upload data on its own, and it has
no telemetry.
