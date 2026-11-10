# Contributing

Thank you for helping. A few rules keep this repository easy to keep in
step with Azure Service Operator (ASO).

## Sign your commits (DCO)

Every commit carries a `Signed-off-by:` line (`git commit -s`), stating the
[Developer Certificate of Origin](https://developercertificate.org/): you
wrote the change, or have the right to submit it under this repository's
MIT licence.

## The copy-upstream rule

Where ASO already has code for a resource, copy it and change as little as
possible:

- keep Microsoft's header and add a `Modified by High Five Labs` note naming
  the source file and what changed (`hack/check-spdx.sh` checks for it);
- list the file in `UPSTREAM.md`;
- never edit the ASO checkout in `third_party/`; a change ASO needs is a
  numbered patch in `patches/azure-service-operator/`, meant to go upstream.

A kind here exists because ASO does not have it, or lacks a field. When ASO
gains it, the kind is deprecated here and users move to ASO's.

## Tasks

The commands below are [Task](https://taskfile.dev) tasks (`brew install
go-task`, 3.54 or later; `task --list` lists them). `Taskfile.yml` is this
repository's; the tasks more than one repository needs (`go:*`) are
factory's `taskfiles/go.yml`, included remotely at a tag and checksum and
fetched with your git credentials on the first run. It knows nothing of
this repository and takes what it needs as variables. Change a shared task
in factory and move the tag here (factory `taskfiles/README.md`).

## Checks

```sh
task upstream   # once: ASO at the pin, with patches/ applied
task check      # pin, go.mod versions, generated files (ASO's generator
                # is rerun), gofmt, vet, licence headers, no fake cloud in
                # the production build, build, unit and end-to-end tests
task generate-arm  # after changing generator/: rerun ASO's generator
                # (heavy; run it on a machine meant for that)
task operator-e2e  # the install on kind with the fake cloud; fails unless the
                # script ends with "all checks passed" and prints the `ok:`
                # lines of tests/operator/required-ok.txt (add a kind's lines there)
```

```sh
task coverage        # the coverage dashboard (docs/coverage/); rerun after a kind
                     # or a pin changes, and commit the result
task coverage-check  # fail if the committed dashboard is stale
```

Adding or changing a CRD changes the dashboard: rerun it. A new hand-written
kind needs an entry in `hack/coverage/ours.yaml`; the tool fails until it has one.

The end-to-end tests need Docker (PostgreSQL runs in a container). No test
uses real Azure credentials; a change that needs them is not testable here
and says so in its description.
