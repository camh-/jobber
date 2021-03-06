# Teleport Interview Challenge

This repository contains my [Teleport interview challenge](doc/challenge.md).

## Continuous integration

This repository uses Github actions for continuous integration on pull requests
and merges to master. Actions taken by CI are defined in the `Makefile` and can
be run locally by running `make ci`. You will need to ensure you have a clean
working directory as a dirty working directory can interfere with the up-to-date
checks for generated files that get committed to the repository. This is most
easily done with `git stash push -u`.

## Requirements

[Hermit] is used to manage any build tools required to use this repository.
Hermit self-installs and it installs any tools when they are first used or when
they are upgraded.

The only dependency is a basic Linux installation (hermit also runs on Darwin
but the code in this repository will not as it will use features specific to the
Linux kernel).

Hermit can be activated in your shell by running:

    source bin/activate-hermit

which will put the `bin/` directory from this repository in your path. When you
are done, run:

    deactivate-hermit

Once the hermit environment is activated, you can run `make help` to see the
`make` targets available. Alternatively, you can run `bin/make help` without
activating hermit, or if you already have `make` installed globally on your
system, you can run `make help` and the Makefile will activate hermit for the
duration of the make targets executed, using the tools in the `bin/` directory.

[Hermit]: https://github.com/cashapp/hermit

## Design

The design is documented in [doc/design.md](doc/design.md). Also included is a
protobuf specification for a service [`JobExecutor`](proto/jobexec.proto).
