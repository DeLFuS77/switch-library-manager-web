# Security policy

## Supported versions

Only the latest release gets security fixes. Docker and Unraid users get them by updating the container.

## Reporting a vulnerability

Please do not open a public issue for a security problem. Report it privately through
[GitHub's private vulnerability reporting](https://github.com/DeLFuS77/switch-library-manager-web/security/advisories/new),
with what an attacker can do, the steps to reproduce it and the version you tested.

You will get an answer within a few days. Once the fix is released, the report is published as a security advisory,
crediting you unless you prefer otherwise.

## What is in scope

- The web app: authentication, sessions, the protection against brute force, access without login on the local
  network, cross-site requests, the save vault (WebDAV) and the handling of files of the library.
- The release programs and the Docker images, and the workflows that build them.

Keys (`prod.keys`, `title.keys`) are never part of the app, its releases or its images: a report that they leak through
the app is in scope.

## Verifying what you download

Every release program and Docker image comes with a signed provenance that proves it was built by this repository's
workflows from the tagged source. See "Verifying the downloads" in the [README](README.md).
