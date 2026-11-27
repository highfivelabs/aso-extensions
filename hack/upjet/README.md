# Upjet facts (generated)

Facts read from Crossplane's Upjet providers, joined onto this
repository's kinds. Don't edit these files: the factory regenerates them
when a provider releases (`factory upjet sync`), and a pull request
brings each change here.

Generators read them; nothing runs a Crossplane provider. For each of our
kinds (or API resources) a file gives the provider's references
(field → target, and what serves the target here), late-init ignores, the
identifier format, list map keys, pointers to where custom code was
needed, whether the maintainers tested it, and its examples. Values are
kept only for hand-written examples; for examples converted from
Terraform's documentation, only the field paths are kept.

## Sources and licences

| File | Provider | Release | Commit | Licence |
| --- | --- | --- | --- | --- |
| [`provider-upjet-azure.json`](provider-upjet-azure.json) | [crossplane-contrib/provider-upjet-azure](https://github.com/crossplane-contrib/provider-upjet-azure) | v2.8.0 | `9f37664bac3d` | Apache-2.0 |

Each provider is its authors' work, licensed under the licence named
above; the facts here are derived from its source at the commit named.
The full licence text is the LICENSE file of that repository at that
commit.
