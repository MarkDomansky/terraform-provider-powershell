* Remember our conversations, summarize them to optimize tokens.
* Ensure unit tests are cross platform.

## Commit messages decide the released version

Versions are **not** set by hand. `semantic-release` reads the commit messages
on the pushed branch and computes the next version
(`.github/workflows/release.yml`, configured by `.releaserc.cjs`). Never edit a
version string by hand and never create a `v*` tag manually - the release
workflow creates the tag itself, only after the E2E suite passes.

Consequences when editing these files:

- Only `main` ever publishes the wiki. `wiki-sync.yml` asserts
  this in the job's `if:` as well as its trigger - do not relax either.
