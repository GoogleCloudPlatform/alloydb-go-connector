# How to Contribute

We'd love to accept your patches and contributions to this project. There are
just a few small guidelines you need to follow.

## Contributor License Agreement

Contributions to this project must be accompanied by a Contributor License
Agreement. You (or your employer) retain the copyright to your contribution;
this simply gives us permission to use and redistribute your contributions as
part of the project. Head over to <https://cla.developers.google.com/> to see
your current agreements on file or to sign a new one.

You generally only need to submit a CLA once, so if you've already submitted one
(even if it was for a different project), you probably don't need to do it
again.

## Code Reviews

All submissions, including submissions by project members, require review. We
use GitHub pull requests for this purpose. Consult
[GitHub Help](https://help.github.com/articles/about-pull-requests/) for more
information on using pull requests.

## Community Guidelines

This project follows [Google's Open Source Community
Guidelines](https://opensource.google/conduct/).

## Testing

Run unit tests without an AlloyDB instance using `go test -short ./...`.

For integration tests, configure the environment variables in `.envrc.example`
and Google Cloud credentials with access to the test instance. Most integration
tests use public IP, so the instance named by `ALLOYDB_INSTANCE_NAME` must have
public IP enabled. IAM authentication tests also require the configured IAM
database user. Credential-option tests require `GOOGLE_APPLICATION_CREDENTIALS`
to point to a credentials JSON file.

To run outside the instance's VPC:

```sh
./scripts/test_system.sh --skip-private-ip
```

This skips private-IP connector tests, direct-connection tests, and Private
Service Connect (PSC) tests. `ALLOYDB_INSTANCE_IP` and
`ALLOYDB_PSC_INSTANCE_URI` are not required for this run. To run only the root
package directly, use `go test -v . -skip-private-ip`. The custom flag is defined
only in the root package; use the script to run all packages with this option.

To include private-network coverage, run from a network with access to the
instance's private IP and the PSC instance, configure `ALLOYDB_INSTANCE_IP` and
`ALLOYDB_PSC_INSTANCE_URI`, and run:

```sh
./scripts/test_system.sh
```

Private-network tests remain enabled by default. `-short` skips all integration
tests regardless of the `-skip-private-ip` setting.
