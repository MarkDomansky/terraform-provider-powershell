<#
.SYNOPSIS
    Renders the docs/ folder into a GitHub wiki working copy.

.DESCRIPTION
    GitHub wikis are a flat page namespace, so the nested docs/ tree is flattened
    onto page names that keep the section visible:

        docs/index.md                      -> Home.md
        docs/resources/script.md           -> Resources-Script.md
        docs/data-sources/script.md        -> Data-Sources-Script.md
        docs/guides/<name>.md              -> Guides-<Name>.md

    Along the way each page gets:
      * its YAML frontmatter stripped (the wiki does not render it),
      * relative links between docs pages rewritten to wiki page names,
      * relative links that escape docs/ rewritten to absolute repo URLs,
      * a generated-file marker so stale pages can be pruned without touching
        pages somebody wrote by hand in the wiki UI,
      * a footer pointing at the source file.

    A _Sidebar.md is generated from the same page set.

    This script only writes files. Committing and pushing is the caller's job
    (see .github/workflows/wiki-sync.yml), which keeps it runnable locally for a
    preview:

        ./.github/scripts/Sync-DocsToWiki.ps1 -WikiPath ../wiki-preview

.PARAMETER DocsPath
    Source docs folder. Defaults to docs/ under the repo root.

.PARAMETER WikiPath
    Existing directory holding the wiki working copy to render into.

.PARAMETER Repository
    owner/repo, used to build absolute URLs for links that leave docs/.
    Defaults to $env:GITHUB_REPOSITORY.

.PARAMETER Branch
    Branch those absolute URLs point at. Defaults to main.
#>
[CmdletBinding()]
param(
    [string] $DocsPath = (Join-Path $PSScriptRoot '../../docs'),
    [Parameter(Mandatory)] [string] $WikiPath,
    [string] $Repository = $env:GITHUB_REPOSITORY,
    [string] $Branch = 'main'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# Written into every generated page. Pages carrying it are ours to overwrite and
# to delete when their source disappears; anything else in the wiki is left alone.
$marker = '<!-- generated-from-docs: do not edit here, edit {0} in the repo -->'

if (-not (Test-Path -LiteralPath $DocsPath)) {
    throw "Docs folder not found: $DocsPath"
}
if (-not (Test-Path -LiteralPath $WikiPath)) {
    throw "Wiki working copy not found: $WikiPath"
}

$docsRoot = (Resolve-Path -LiteralPath $DocsPath).Path
$wikiRoot = (Resolve-Path -LiteralPath $WikiPath).Path
$repoUrl = if ($Repository) { "https://github.com/$Repository" } else { $null }

function Get-RelativeDocPath {
    param([string] $FullPath)
    $rel = $FullPath.Substring($docsRoot.Length).TrimStart('\', '/')
    return $rel -replace '\\', '/'
}

# docs-relative path -> wiki page name (no .md).
function Get-WikiPageName {
    param([string] $RelativePath)

    if ($RelativePath -eq 'index.md') { return 'Home' }

    $segments = ($RelativePath -replace '\.md$', '') -split '/'
    $titled = foreach ($segment in $segments) {
        $words = $segment -split '[-_ ]' | Where-Object { $_ }
        ($words | ForEach-Object {
            $_.Substring(0, 1).ToUpperInvariant() + $_.Substring(1)
        }) -join '-'
    }
    return $titled -join '-'
}

# Normalize 'a/b/../c' style paths by hand: the targets do not necessarily exist
# on disk (they may point outside the repo checkout), so Resolve-Path is out.
function Resolve-RelativePath {
    param([string] $BaseDir, [string] $Target)

    $parts = @()
    if ($BaseDir) { $parts += ($BaseDir -split '/' | Where-Object { $_ }) }
    $parts += ($Target -split '/' | Where-Object { $_ })

    $stack = [System.Collections.Generic.List[string]]::new()
    foreach ($part in $parts) {
        switch ($part) {
            '.' { continue }
            '..' { if ($stack.Count -gt 0) { $stack.RemoveAt($stack.Count - 1) } else { $stack.Add('..') } }
            default { $stack.Add($part) }
        }
    }
    return ($stack -join '/')
}

# Build the page map first: link rewriting needs to know every page up front.
$docFiles = Get-ChildItem -LiteralPath $docsRoot -Filter '*.md' -File -Recurse | Sort-Object FullName
if (-not $docFiles) { throw "No markdown files found under $docsRoot" }

$pages = foreach ($file in $docFiles) {
    $rel = Get-RelativeDocPath $file.FullName
    [pscustomobject]@{
        SourcePath = $file.FullName
        RelativePath = $rel
        Directory = if ($rel -match '/') { $rel -replace '/[^/]+$', '' } else { '' }
        PageName = Get-WikiPageName $rel
        Section = if ($rel -match '/') { ($rel -split '/')[0] } else { '' }
    }
}

$pageByRelativePath = @{}
foreach ($page in $pages) { $pageByRelativePath[$page.RelativePath] = $page.PageName }

function Convert-Link {
    param([string] $Target, [string] $BaseDir)

    # Absolute, protocol-relative, in-page anchors and wiki-native links: as-is.
    if ($Target -match '^([a-z][a-z0-9+.-]*:|//|#)') { return $Target }

    $anchor = ''
    $path = $Target
    if ($Target -match '^(?<path>[^#]*)(?<anchor>#.*)$') {
        $path = $Matches['path']
        $anchor = $Matches['anchor']
    }
    if (-not $path) { return $Target }

    $resolved = Resolve-RelativePath -BaseDir $BaseDir -Target $path

    # Still inside docs/ and a page we publish -> wiki page name.
    if ($pageByRelativePath.ContainsKey($resolved)) {
        return "$($pageByRelativePath[$resolved])$anchor"
    }

    # Escapes docs/ (or points at a non-published file) -> absolute repo URL.
    if (-not $repoUrl) { return $Target }
    $repoRelative = Resolve-RelativePath -BaseDir "docs/$BaseDir".TrimEnd('/') -Target $path
    $repoRelative = $repoRelative -replace '^(\.\./)+', ''
    $kind = if ([System.IO.Path]::GetExtension($repoRelative)) { 'blob' } else { 'tree' }
    return "$repoUrl/$kind/$Branch/$repoRelative$anchor"
}

function Convert-PageContent {
    param([string] $Content, [string] $BaseDir)

    # Strip leading YAML frontmatter ((?s) so . spans the newlines inside it).
    $Content = $Content -replace '(?s)^﻿?---\r?\n.*?\r?\n---\r?\n', ''

    # Rewrite inline links/images: ](target) and ](target "title").
    $linkPattern = '(?<prefix>\]\()(?<target>[^)\s]+)(?<suffix>(\s+"[^"]*")?\))'
    $Content = [regex]::Replace($Content, $linkPattern, {
        param($match)
        $target = Convert-Link -Target $match.Groups['target'].Value -BaseDir $BaseDir
        "$($match.Groups['prefix'].Value)$target$($match.Groups['suffix'].Value)"
    })

    # Rewrite reference-style definitions: [label]: target
    $refPattern = '(?m)^(?<prefix>\[[^\]]+\]:\s+)(?<target>\S+)'
    $Content = [regex]::Replace($Content, $refPattern, {
        param($match)
        $target = Convert-Link -Target $match.Groups['target'].Value -BaseDir $BaseDir
        "$($match.Groups['prefix'].Value)$target"
    })

    return $Content
}

$written = [System.Collections.Generic.List[string]]::new()

foreach ($page in $pages) {
    $raw = Get-Content -LiteralPath $page.SourcePath -Raw
    $body = Convert-PageContent -Content $raw -BaseDir $page.Directory

    $sourceLink = if ($repoUrl) {
        "[``docs/$($page.RelativePath)``]($repoUrl/blob/$Branch/docs/$($page.RelativePath))"
    } else {
        "``docs/$($page.RelativePath)``"
    }

    $lines = @(
        ($marker -f "docs/$($page.RelativePath)")
        ''
        $body.TrimEnd()
        ''
        '---'
        ''
        "*This page is generated from $sourceLink. Edits made here are overwritten.*"
        ''
    )

    $target = Join-Path $wikiRoot "$($page.PageName).md"
    Set-Content -LiteralPath $target -Value ($lines -join "`n") -NoNewline:$false -Encoding utf8
    $written.Add("$($page.PageName).md")
    Write-Host "docs/$($page.RelativePath) -> $($page.PageName).md"
}

# Sidebar: Home first, then one group per docs subfolder.
$sidebar = [System.Collections.Generic.List[string]]::new()
$sidebar.Add(($marker -f 'docs/'))
$sidebar.Add('')

$homePage = $pages | Where-Object { $_.PageName -eq 'Home' }
if ($homePage) { $sidebar.Add('- [[Home]]') }

foreach ($section in ($pages | Where-Object { $_.Section } | Select-Object -ExpandProperty Section -Unique | Sort-Object)) {
    $words = $section -split '[-_]' | Where-Object { $_ }
    $heading = ($words | ForEach-Object { $_.Substring(0, 1).ToUpperInvariant() + $_.Substring(1) }) -join ' '
    $sidebar.Add("- $heading")
    foreach ($page in ($pages | Where-Object { $_.Section -eq $section } | Sort-Object PageName)) {
        $leaf = ($page.RelativePath -split '/')[-1] -replace '\.md$', ''
        $words = $leaf -split '[-_]' | Where-Object { $_ }
        $label = ($words | ForEach-Object { $_.Substring(0, 1).ToUpperInvariant() + $_.Substring(1) }) -join ' '
        $sidebar.Add("  - [[$label|$($page.PageName)]]")
    }
}
$sidebar.Add('')

Set-Content -LiteralPath (Join-Path $wikiRoot '_Sidebar.md') -Value ($sidebar -join "`n") -Encoding utf8
$written.Add('_Sidebar.md')

# Prune pages we generated on an earlier run whose source is gone. Pages without
# the marker were authored in the wiki UI and are left untouched.
$markerPrefix = '<!-- generated-from-docs:'
foreach ($existing in (Get-ChildItem -LiteralPath $wikiRoot -Filter '*.md' -File)) {
    if ($written -contains $existing.Name) { continue }
    $firstLine = Get-Content -LiteralPath $existing.FullName -TotalCount 1
    if ($firstLine -and $firstLine.StartsWith($markerPrefix)) {
        Remove-Item -LiteralPath $existing.FullName -Force
        Write-Host "removed stale page $($existing.Name)"
    }
}

Write-Host "Rendered $($written.Count) page(s) into $wikiRoot"
