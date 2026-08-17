/**
 * semantic-release configuration.
 *
 *   beta -> x.y.z-beta.N   prerelease; every squash-merged PR lands here
 *   main -> x.y.z          GA, cut by merging beta into main
 *
 * Merge beta into main with a REAL merge commit, never a squash: the analyzer
 * derives the GA bump from the individual conventional commits, and a squash
 * collapses them into one message and one (probably wrong) bump.
 *
 * `main` is the required non-prerelease branch, so no `stable` placeholder is
 * needed here. semantic-release fails with ERELEASEBRANCHES when every
 * configured branch is a prerelease - that is why the retired always-alpha dev
 * repo needed one - and it silently DROPS configured branches that do not
 * exist on the remote, so both branches above must exist.
 *
 * No plugin below writes back to the repo (no @semantic-release/git, no
 * changelog file), so releasing never moves a branch and no back-merge into
 * beta is needed after a GA.
 *
 * This must stay a .cjs file, not YAML: cosmiconfig searches
 * .releaserc.yaml/.yml BEFORE .releaserc.js/.cjs, so a leftover .releaserc.yml
 * would win and silently shadow this file. Do not re-add one.
 */

module.exports = {
  branches: ['main', { name: 'beta', prerelease: 'beta' }],

  tagFormat: 'v${version}',

  plugins: [
    // `feat` -> minor, everything else -> patch. The `**` catch-all makes any
    // other commit message a patch, so free-form commits still cut a release
    // instead of silently releasing nothing. Delete that last rule to release
    // only on conventional commits. To land a commit without releasing, put
    // [skip ci] in the message.
    //
    // BREAKING -> MINOR, ON PURPOSE. A breaking change would normally bump the
    // major, which from 0.x means jumping straight to 1.0.0 - a commitment to
    // 1.0 API stability made by accident, by one commit message, now that
    // `main` cuts real GAs. Mapping it to minor keeps the provider in 0.x
    // (0.1.x -> 0.2.0) where breaking changes are expected. This only changes
    // the VERSION: `BREAKING CHANGE:` footers are still rendered by
    // release-notes-generator, so the release notes keep shouting about them.
    //
    // Going 1.0 is therefore a deliberate act: delete this rule (restoring
    // `release: 'major'`) or force the version once, then merge to main.
    //
    // preset: the default (angular) preset does NOT understand the `feat!:`
    // shorthand - such a commit fails to parse and drops to the catch-all as a
    // patch. The conventionalcommits preset handles it. It is not bundled with
    // semantic-release, so both workflow steps install it via extra_plugins.
    [
      '@semantic-release/commit-analyzer',
      {
        preset: 'conventionalcommits',
        releaseRules: [
          { breaking: true, release: 'minor' },
          { type: 'feat', release: 'minor' },
          { revert: true, release: 'patch' },
          { message: '**', release: 'patch' },
        ],
      },
    ],

    ['@semantic-release/release-notes-generator', { preset: 'conventionalcommits' }],

    // Creates the tag and the GitHub release, and attaches the artifacts that
    // the E2E job already validated. Signatures are only present when the GPG
    // secrets are configured; a glob that matches nothing is skipped.
    [
      '@semantic-release/github',
      {
        assets: [
          { path: 'dist/*.zip' },
          { path: 'dist/*_SHA256SUMS' },
          { path: 'dist/*_SHA256SUMS.sig' },
          { path: 'dist/*_manifest.json' },
        ],
        successComment: false,
        failComment: false,
        labels: false,
        releasedLabels: false,
      },
    ],
  ],
};
