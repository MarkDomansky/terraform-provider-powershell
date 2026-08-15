/**
 * semantic-release configuration.
 *
 * This one file ships to BOTH repos - the public repo's contents are a squashed
 * promotion of this tree (see .github/workflows/promote-beta.yml) - but `main`
 * means something different in each, so the branch config is chosen at runtime
 * from GITHUB_REPOSITORY:
 *
 *   terraform-provider-powershell-dev (private)
 *     main    -> x.y.z-alpha.N   prerelease, always; this repo never cuts a GA
 *     stable  -> placeholder release branch (nothing is released from it)
 *
 *   terraform-provider-powershell (public)
 *     beta    -> x.y.z-beta.N    prerelease; where promotions land
 *     main    -> x.y.z           GA, cut by merging beta into main
 *
 * semantic-release requires at least one non-prerelease ("release") branch in
 * every configuration and silently DROPS configured branches that do not exist
 * on the remote - which is why `stable` has to exist on the dev remote even
 * though nothing is ever released from it (otherwise: ERELEASEBRANCHES).
 *
 * This must stay a .cjs file, not YAML: cosmiconfig searches
 * .releaserc.yaml/.yml BEFORE .releaserc.js/.cjs, so a leftover .releaserc.yml
 * would win and silently shadow this file. Do not re-add one.
 */

const repo = (process.env.GITHUB_REPOSITORY || '').toLowerCase();

// Default to the dev (always-prerelease) config when GITHUB_REPOSITORY is not
// set - i.e. a local run. That is the conservative default: the worst a
// mis-detection can do here is cut an alpha, never an unintended GA.
const isPublicRepo = repo !== '' && !repo.endsWith('-dev');

const branches = isPublicRepo
  ? ['main', { name: 'beta', prerelease: 'beta' }]
  : ['stable', { name: 'main', prerelease: 'alpha' }];

module.exports = {
  branches,

  tagFormat: 'v${version}',

  plugins: [
    // Conventional commits behave as usual (feat -> minor, BREAKING CHANGE: or
    // a `!` after the type -> major). The `**` catch-all makes every other
    // commit message a patch, so free-form commits still cut a release instead
    // of silently releasing nothing. Delete that last rule to release only on
    // conventional commits. To land a commit without releasing, put [skip ci]
    // in the message.
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
          { breaking: true, release: 'major' },
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
