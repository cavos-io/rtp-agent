# rtp-agent docs website

This website is built with [Docusaurus](https://docusaurus.io/) under `docs/website`.

The lockfile is `package-lock.json`, so use `npm` for local development and CI.

## Installation

```bash
npm ci
```

## Local Development

```bash
npm run start
```

This starts a local development server. Most documentation changes are reflected without a restart.

Before committing documentation changes, run:

```bash
npm run typecheck
npm run build
```

## Build

```bash
npm run build
```

This generates static content in `build`.

## Deployment

Use the Docusaurus deploy script only from an environment configured for the target host:

```bash
npm run deploy
```
